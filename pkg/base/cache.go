package base

import (
	"context"
	"github.com/go-redis/cache/v8"
	"gorm.io/gorm"
	"log"
	"strconv"
	"time"
	"treehollow-v3-backend/pkg/consts"
	"treehollow-v3-backend/pkg/logger"
	"treehollow-v3-backend/pkg/utils"
)

var tokenCache *cache.Cache
var commentCache *cache.Cache

const CommentCacheExpireTime = 5 * time.Hour
const TOKENCacheExpireTime = 1 * time.Minute

func initCache() {
	tokenCache = cache.New(&cache.Options{Redis: redisClient})
	commentCache = cache.New(&cache.Options{Redis: redisClient})
}

func GetUserWithCache(token string) (User, error) {
	ctx := context.TODO()
	var user User
	err := tokenCache.Get(ctx, "token"+token, &user)
	if err == nil {
		return user, nil
	} else {
		subQuery := db.Model(&Device{}).Distinct().
			Where("token = ? and created_at > ?", token, utils.GetEarliestAuthenticationTime()).
			Select("user_id")
		err = db.Where("id = (?)", subQuery).First(&user).Error
		if err == nil {
			err = tokenCache.Set(&cache.Item{
				Ctx:   ctx,
				Key:   "token" + token,
				Value: &user,
				TTL:   TOKENCacheExpireTime,
			})
		}
		return user, err
	}
}

func DelUserCache(token string) error {
	ctx := context.TODO()
	err := tokenCache.Delete(ctx, "token"+token)
	if err != nil {
		log.Printf("DelUserCache error: %s\n", err)
	}
	return err
}

func GetCommentsWithCache(post *Post, now time.Time) ([]Comment, error) {
	pid := post.ID
	if !NeedCacheComment(post, now) {
		return GetComments(pid)
	}

	ctx := context.TODO()
	pidStr := strconv.Itoa(int(pid))
	var comments []Comment
	err := commentCache.Get(ctx, "pid"+pidStr, &comments)
	if err == nil {
		return comments, err
	} else {
		comments, err = GetComments(pid)
		if err == nil {
			err = commentCache.Set(&cache.Item{
				Ctx:   ctx,
				Key:   "pid" + pidStr,
				Value: &comments,
				TTL:   CommentCacheExpireTime,
			})
		}
		return comments, err
	}
}

func GetMultipleCommentsWithCache(tx *gorm.DB, posts []Post, now time.Time) (map[int32][]Comment, *logger.InternalError) {
	ctx := context.TODO()
	rtn := make(map[int32][]Comment)
	var noCachePidsArray []int32
	// 区分哪些需要从缓存读，哪些需要从DB读
	for _, post := range posts {
		pid := post.ID
		// 如果不需要缓存，则加入DB读取列表
		if !NeedCacheComment(&post, now) {
			noCachePidsArray = append(noCachePidsArray, pid)
			continue
		}

		// 尝试从缓存读取
		pidStr := strconv.Itoa(int(pid))
		var comments []Comment
		err := commentCache.Get(ctx, "pid"+pidStr, &comments)
		if err == nil {
			rtn[pid] = comments // 缓存命中
		} else {
			noCachePidsArray = append(noCachePidsArray, pid) // 缓存未命中，加入DB读取列表
		}
	}

	// 从DB一次性读取所有未命中缓存的评论
	if len(noCachePidsArray) > 0 {
		commentsFromDB, err := GetMultipleComments(tx, noCachePidsArray)
		if err != nil {
			return nil, logger.NewError(err, "SQLGetMultipleCommentsFailed", consts.DatabaseReadFailedString)
		}
		// 按 post_id 分组
		commentsMap := make(map[int32][]Comment)
		for _, comment := range commentsFromDB {
			commentsMap[comment.PostID] = append(commentsMap[comment.PostID], comment)
		}

		// 将DB读取的结果合并到最终结果中，并写回缓存
		for _, pid := range noCachePidsArray {
			commentsForPid, ok := commentsMap[pid]
			if !ok {
				// 如果DB中也没有，则为空slice
				commentsForPid = []Comment{}
			}
			rtn[pid] = commentsForPid

			// 只有当该post需要缓存时才进行设置
			postMap := make(map[int32]Post)
			for _, p := range posts {
				postMap[p.ID] = p
			}
			if p, exists := postMap[pid]; exists && NeedCacheComment(&p, now) {
				err := commentCache.Set(&cache.Item{
					Ctx:   ctx,
					Key:   "pid" + strconv.Itoa(int(pid)),
					Value: &commentsForPid, // 缓存从DB查到的结果
					TTL:   CommentCacheExpireTime,
				})
				if err != nil {
					// 缓存设置失败不应阻塞主流程，记录日志即可
					log.Printf("CommentCacheSetFailed for pid %d: %v", pid, err)
				}
			}
		}
	}
	return rtn, nil
}

func DelCommentCache(pid int) error {
	ctx := context.TODO()
	err := commentCache.Delete(ctx, "pid"+strconv.Itoa(pid))
	if err != nil {
		log.Printf("DelCommentCache error: %s\n", err)
	}
	return err
}

func NeedCacheComment(post *Post, now time.Time) bool {
	return now.Before(post.CreatedAt.AddDate(0, 0, 365))
	//return now.Before(post.CreatedAt.AddDate(0, 0, 2))
}