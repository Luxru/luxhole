package base

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"treehollow-v3-backend/pkg/consts"
	"treehollow-v3-backend/pkg/model"
	"treehollow-v3-backend/pkg/utils"

	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const HotListKey = "webhole:hot_list:zset"

var db *gorm.DB

func AutoMigrateDb() {
	err := db.AutoMigrate(&User{}, &Email{},
		&Device{}, &PushSettings{}, &Vote{},
		&VerificationCode{}, &Post{}, &PostCommenter{}, &PushMessage{},
		&Comment{}, &Attention{}, &Report{}, &SystemMessage{}, Ban{})
	utils.FatalErrorHandle(&err, "error migrating database!")
}

func InitDb() {
	err2 := initRedis()
	utils.FatalErrorHandle(&err2, "error init redis")
	initCache()

	logFile, err := os.OpenFile("sql.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	utils.FatalErrorHandle(&err, "error init sql log file")
	mw := io.MultiWriter(os.Stdout, logFile)
	logLevel := logger.Silent
	if viper.GetBool("is_debug") {
		logLevel = logger.Info
	}
	newLogger := logger.New(
		log.New(mw, "\r\n", log.LstdFlags), // io writer
		logger.Config{
			SlowThreshold: time.Millisecond * 500, // Slow SQL threshold
			LogLevel:      logLevel,               // Log level
			Colorful:      true,
		},
	)

	db, err = gorm.Open(mysql.Open(
		viper.GetString("sql_source")+"?charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   newLogger,
	})
	sqlDB, err := db.DB()
	if err != nil {
		panic(err)
	}
	sqlDB.SetMaxOpenConns(150)
	utils.FatalErrorHandle(&err, "error opening sql db")
}

func GetDb(unscoped bool) *gorm.DB {
	if unscoped {
		return db.Unscoped()
	}
	return db
}

func ListPosts(tx *gorm.DB, p int, user *User) (posts []Post, err error) {
	offset := (p - 1) * consts.PageSize
	limit := consts.PageSize
	pinnedPids := viper.GetIntSlice("pin_pids")
	if CanViewDeletedPost(user) {
		tx = tx.Unscoped()
	}
	if len(pinnedPids) == 0 {
		err = tx.Order("id desc").Limit(limit).Offset(offset).Find(&posts).Error
	} else {
		err = tx.Where("id not in ?", pinnedPids).Order("id desc").Limit(limit).Offset(offset).
			Find(&posts).Error
	}
	return
}

func ListMsgs(p int, minId int32, userId int32, pushOnly bool) (msgs []PushMessage, err error) {
	offset := (p - 1) * consts.MsgPageSize
	limit := consts.MsgPageSize
	tx := db
	if pushOnly {
		tx = tx.Where("do_push = ?", true)
	}
	err = tx.Where("user_id = ? and id > ?", userId, minId).Order("id desc").Limit(limit).Offset(offset).
		Find(&msgs).Error
	return
}

func GetComments(pid int32) ([]Comment, error) {
	var comments []Comment
	err := db.Unscoped().Where("post_id = ?", pid).Order("id asc").Find(&comments).Error
	return comments, err
}

func GetCommentsPaginated(pid int32, page int, pageSize int) ([]Comment, int64, error) {
	var comments []Comment
	var total int64
	offset := (page - 1) * pageSize

	tx := db.Unscoped().Model(&Comment{}).Where("post_id = ?", pid)

	// 先获取总数
	err := tx.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 再获取分页数据
	err = tx.Order("id asc").Limit(pageSize).Offset(offset).Find(&comments).Error
	return comments, total, err
}

func GetMultipleComments(tx *gorm.DB, pids []int32) ([]Comment, error) {
	var comments []Comment
	err := tx.Unscoped().Where("post_id in (?)", pids).Order("id asc").Find(&comments).Error
	return comments, err
}

func SearchPosts(page int, keywords string, limitPids []int32, user User, order model.SearchOrder,
	includeComment bool, beforeTimestamp int64, afterTimestamp int64) (posts []Post, err error) {
	canViewDelete := CanViewDeletedPost(&user)
	var thePost Post
	var err2 error
	pid := -1
	if page == 1 {
		if strings.HasPrefix(keywords, "#") {
			pid, err2 = strconv.Atoi(keywords[1:])
		} else {
			pid, err2 = strconv.Atoi(keywords)
		}
		if err2 == nil {
			err2 = GetDb(canViewDelete).First(&thePost, int32(pid)).Error
		}
	}
	offset := (page - 1) * consts.SearchPageSize
	limit := consts.SearchPageSize

	tx := GetDb(canViewDelete)
	if limitPids != nil {
		tx = tx.Where("id in ?", limitPids)
	}

	subSearch := func(tx0 *gorm.DB, isTag bool) *gorm.DB {
		if isTag {
			return tx0.Where("tag = ?", keywords[1:])
		}
		replacedKeywords := "+" + strings.ReplaceAll(keywords, " ", " +")
		return tx0.Where("match(text) against(? IN BOOLEAN MODE)", replacedKeywords)
	}

	if canViewDelete && keywords == "dels" {
		subQuery1 := db.Unscoped().Model(&Report{}).Distinct().
			Where("type in (?) and user_id != reported_user_id and post_id = posts.id",
				[]ReportType{UserDelete, AdminDeleteAndBan}).Select("post_id")
		err = db.Unscoped().Where("id in (?)", subQuery1).
			Order(order.ToString()).Limit(limit).Offset(offset).Find(&posts).Error
	} else {
		var subQuery2 *gorm.DB
		if includeComment {
			subQuery := subSearch(GetDb(canViewDelete).Model(&Comment{}).Distinct(),
				strings.HasPrefix(keywords, "#")).
				Select("post_id")
			subQuery2 = subSearch(GetDb(canViewDelete), strings.HasPrefix(keywords, "#")).
				Or("id in (?)", subQuery)
		} else {
			subQuery2 = subSearch(GetDb(canViewDelete), strings.HasPrefix(keywords, "#"))
		}

		if beforeTimestamp > 0 {
			tx = tx.Where("created_at < ?", time.Unix(beforeTimestamp, 0).In(consts.TimeLoc))
		}
		if afterTimestamp > 0 {
			tx = tx.Where("created_at >= ?", time.Unix(afterTimestamp, 0).In(consts.TimeLoc))
		}
		if pid > 0 {
			tx = tx.Where("id != ?", pid)
		}

		err = tx.Where(subQuery2).Order(order.ToString()).Limit(limit).Offset(offset).Find(&posts).Error
	}

	if err2 == nil && page == 1 {
		posts = append([]Post{thePost}, posts...)
	}
	return
}

func GetVerificationCode(emailHash string) (string, int64, int, error) {
	var vc VerificationCode
	err := db.Where("email_hash = ?", emailHash).First(&vc).Error
	return vc.Code, vc.UpdatedAt.Unix(), vc.FailedTimes, err
}

func SavePost(uid int32, text string, tag string, typ string, filePath string, metaStr string, voteData string) (id int32, err error) {
	post := Post{Tag: tag, UserID: uid, Text: text, Type: typ, FilePath: filePath, LikeNum: 0, ReplyNum: 0,
		ReportNum: 0, FileMetadata: metaStr, VoteData: voteData}
	err = db.Save(&post).Error
	id = post.ID
	return
}

func GetHotPosts() (posts []Post, err error) {
	err = db.Order("like_num*3+reply_num+UNIX_TIMESTAMP(created_at)/1800-report_num*10 DESC").
        Limit(200).Find(&posts).Error
	return
}

func SaveComment(tx *gorm.DB, uid int32, text string, tag string, typ string, filePath string, pid int32, replyTo int32, name string,
	metaStr string) (id int32, err error) {
	comment := Comment{Tag: tag, UserID: uid, PostID: pid, ReplyTo: replyTo, Text: text, Type: typ, FilePath: filePath,
		Name: name, FileMetadata: metaStr}
	err = tx.Save(&comment).Error
	id = comment.ID
	if err == nil {
		err = DelCommentCache(int(pid))
	}
	return
}

func GenCommenterName(tx *gorm.DB, dzUserID int32, czUserID int32, postID int32, names0 []string, names1 []string) (string, error) {
    // 1. 洞主判断，此逻辑不变且高效
    if dzUserID == czUserID {
        return consts.DzName, nil
    }

    // 2. 尝试直接从映射表中查找该用户的匿名
    var mapping PostCommenter
    err := tx.Where("post_id = ? AND user_id = ?", postID, czUserID).First(&mapping).Error

    // 2a. 找到了，说明是老评论者，直接返回名字
    if err == nil {
        return mapping.CommenterName, nil
    }

    // 2b. 没找到 (gorm.ErrRecordNotFound)，说明是新评论者，需要生成新名字
    if err != gorm.ErrRecordNotFound {
        // 如果是其他数据库错误，则直接返回
        return "", err
    }

    // 3. 为新评论者生成名字（核心优化部分）
    // 使用事务 + 行锁来保证并发安全
    var post Post
    // 使用 FOR UPDATE 锁定帖子行，防止多个新评论者同时读取到旧的计数值
    if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&post, postID).Error; err != nil {
        return "", err
    }

    // 计数值+1
    newCount := post.DistinctCommenterCount + 1
    newName := utils.GetCommenterName(int(newCount), names0, names1)

    // 创建新的映射关系
    newMapping := PostCommenter{
        PostID:        postID,
        UserID:        czUserID,
        CommenterName: newName,
    }
    if err := tx.Create(&newMapping).Error; err != nil {
        return "", err
    }

    // 更新帖子表中的计数值
    if err := tx.Model(&post).Update("distinct_commenter_count", newCount).Error; err != nil {
        // 如果这里失败，事务会自动回滚，保证数据一致性
        return "", err
    }

    return newName, nil
}

func GetBannedTime(tx *gorm.DB, uid int32, startTime int64) (times int64, err error) {
	err = tx.Model(&Ban{}).Where("user_id = ? and expire_at > ?", uid, startTime).Count(&times).Error
	return
}

func calcBanExpireTime(times int64) int64 {
	return utils.GetTimeStamp() + (times+1)*86400
}

func generateBanReason(report Report, originalText string) (rtn string) {
	var pre string
	if report.IsComment {
		pre = "您的树洞评论#" + strconv.Itoa(int(report.PostID)) + "-" + strconv.Itoa(int(report.CommentID))
	} else {
		pre = "您的树洞#" + strconv.Itoa(int(report.PostID))
	}
	switch report.Type {
	case UserReport:
		rtn = pre + "\n\"" + originalText + "\"\n因为用户举报过多被删除。"
	case AdminDeleteAndBan:
		rtn = pre + "\n\"" + originalText + "\"\n被管理员删除。管理员的删除理由是：【" + report.Reason + "】。"
	}
	return
}

func DeleteByReport(tx *gorm.DB, report Report) (err error) {
	if report.IsComment {
		err = tx.Where("id = ?", report.CommentID).Delete(&Comment{}).Error
		if err == nil {
			err = tx.Model(&Post{}).Where("id = ?", report.PostID).Update("reply_num",
				gorm.Expr("reply_num - 1")).Error
			if err == nil {
				// 评论数变化，更新热榜分数
				if e := UpdateHotListScore(tx, report.PostID); e != nil {
					log.Printf("Error updating hot list score on comment delete: %v", e)
				}
				err = DelCommentCache(int(report.PostID))
				go func() {
					SendDeletionToPushService(report.CommentID)
				}()
			}
		}
	} else {
		err = tx.Where("id = ?", report.PostID).Delete(&Post{}).Error
		if err == nil {
			// 帖子被删除，从热榜中移除
			GetRedisClient().ZRem(context.Background(), HotListKey, strconv.Itoa(int(report.PostID)))
		}
	}
	return
}

func DeleteAndBan(tx *gorm.DB, report Report, text string) (err error) {
	err = DeleteByReport(tx, report)
	if err == nil {
		times, err2 := GetBannedTime(tx, report.ReportedUserID, 0)
		if err2 == nil {
			tx.Create(&Ban{
				UserID:   report.ReportedUserID,
				ReportID: report.ID,
				Reason:   generateBanReason(report, text),
				ExpireAt: calcBanExpireTime(times),
			})
		}
	}
	return
}

func SetTagByReport(tx *gorm.DB, report Report) (err error) {
	if report.IsComment {
		err = tx.Model(&Comment{}).Where("id = ?", report.CommentID).
			Update("tag", report.Reason).Error
		if err == nil {
			err = tx.Model(&Post{}).Where("id = ?", report.PostID).
				Update("updated_at", time.Now()).Error
			if err == nil {
				err = DelCommentCache(int(report.PostID))
			}
		}
	} else {
		err = tx.Model(&Post{}).Where("id = ?", report.PostID).
			Update("tag", report.Reason).Error
	}
	return
}

func UnbanByReport(tx *gorm.DB, report Report) (err error) {
	var ban Ban
	subQuery := tx.Model(&Report{}).Distinct().
		Where("post_id = ? and comment_id = ? and is_comment = ? and type in (?)",
			report.PostID, report.CommentID, report.IsComment,
			[]ReportType{UserReport, AdminDeleteAndBan}).
		Select("id")
	err = tx.Model(&Ban{}).Where("report_id in (?)", subQuery).First(&ban).Error
	if err == nil {
		err = tx.Delete(&ban).Error
	}
	return
}

func ComputeScore(post *Post)(score float64){
	score = float64(post.LikeNum*3+post.ReplyNum) +
			float64(post.CreatedAt.Unix())/1800.0 -
			float64(post.ReportNum*10)
	return
}

// UpdateHotListScore 计算并更新单个帖子的热度分数
func UpdateHotListScore(tx *gorm.DB, postID int32) error {
	var post Post
	// 获取最新的帖子数据
	if err := tx.First(&post, postID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 帖子可能已被删除，从热榜中移除
			GetRedisClient().ZRem(context.Background(), HotListKey, strconv.Itoa(int(postID)))
			return nil
		}
		return err
	}

	// 热度分计算公式
	score := ComputeScore(&post)

	// 更新到 Redis ZSET
	return GetRedisClient().ZAdd(context.Background(), HotListKey, &redis.Z{
		Score:  score,
		Member: strconv.Itoa(int(postID)),
	}).Err()
}

// GetPostsByIDsInOrder 根据ID列表获取帖子，并保持传入的顺序
func GetPostsByIDsInOrder(tx *gorm.DB, postIDs []int32) ([]Post, error) {
	if len(postIDs) == 0 {
		return []Post{}, nil
	}

	var posts []Post
	// 将 ID 列表转换为逗号分隔的字符串，例如 [10, 2, 5] -> "10,2,5"
	idStr := strings.Trim(strings.Replace(fmt.Sprint(postIDs), " ", ",", -1), "[]")
	// 使用 MySQL 的 FIND_IN_SET 函数来保证返回的顺序与 idStr 中的顺序一致
	orderClause := fmt.Sprintf("FIND_IN_SET(id, '%s')", idStr)

	err := tx.Where("id IN (?)", postIDs).Order(orderClause).Find(&posts).Error
	return posts, err
}
