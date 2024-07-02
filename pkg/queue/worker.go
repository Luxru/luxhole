package queue

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"
	"treehollow-v3-backend/pkg/base"
	// "treehollow-v3-backend/pkg/mail"
	"treehollow-v3-backend/pkg/model"
	"treehollow-v3-backend/pkg/utils"

	"gorm.io/gorm"

	"github.com/go-redis/redis/v8"
	"github.com/vmihailenco/msgpack/v5"
)

// StartWorkers 启动所有后台 worker
func StartWorkers() {
	log.Println("Starting background workers...")
	go startDefaultQueueWorker()
	go startDelayedQueueScheduler()
}

// startDefaultQueueWorker 消费默认队列中的任务
func startDefaultQueueWorker() {
	client := base.GetRedisClient()
	for {
		// 使用 BRPOP 进行阻塞式读取
		result, err := client.BRPop(context.Background(), 0, DefaultQueue).Result()
		if err != nil {
			log.Printf("Error reading from default queue: %v. Retrying in 5s.", err)
			time.Sleep(5 * time.Second)
			continue
		}

		// result 是一个 string slice, result[0] 是 key, result[1] 是 value
		var task Task
		if err := msgpack.Unmarshal([]byte(result[1]), &task); err != nil {
			log.Printf("Error unmarshalling task: %v", err)
			continue
		}

		if err := processTask(task); err != nil {
			log.Printf("Error processing task of type %s: %v", task.Type, err)
		}
	}
}

// startDelayedQueueScheduler 轮询延迟队列，将到期的任务移至默认队列
func startDelayedQueueScheduler() {
	client := base.GetRedisClient()
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now().Unix()
		// 查找所有 score (执行时间) 小于等于当前时间的任务
		tasks, err := client.ZRangeByScore(context.Background(), DelayedQueue, &redis.ZRangeBy{
			Min: "-inf",
			Max: strconv.FormatInt(now, 10),
		}).Result()

		if err != nil {
			log.Printf("Error polling delayed queue: %v", err)
			continue
		}

		if len(tasks) == 0 {
			continue
		}

		// 使用 pipeline 提高效率
		pipe := client.Pipeline()
		for _, taskStr := range tasks {
			pipe.LPush(context.Background(), DefaultQueue, taskStr)
		}
		pipe.ZRem(context.Background(), DelayedQueue, tasks) // 从延迟队列中移除

		if _, err := pipe.Exec(context.Background()); err != nil {
			log.Printf("Error moving tasks from delayed to default queue: %v", err)
		}
	}
}

// processTask 根据任务类型分发任务
func processTask(task Task) error {
	switch task.Type {
	case TaskSendEmail:
		return handleSendEmail(task.Payload)
	case TaskPushNotification:
		return handlePushNotification(task.Payload)
	default:
		return errors.New("unknown task type: " + string(task.Type))
	}
}

// handleSendEmail 处理发送邮件任务
func handleSendEmail(payloadBytes []byte) error {
	var payload EmailPayload
	if err := msgpack.Unmarshal(payloadBytes, &payload); err != nil {
		return err
	}

	log.Printf("Sending email to %s, type: %s, code:%s nonce: %s", payload.Recipient, payload.Type, payload.Code, payload.Nonce)
	return nil
	// var err error
	// switch payload.Type {
	// case "validation":
	// 	err = mail.SendValidationEmail(payload.Code, payload.Recipient)
	// case "unregister":
	// 	err = mail.SendUnregisterValidationEmail(payload.Code, payload.Recipient)
	// case "nonce":
	// 	err = mail.SendPasswordNonceEmail(payload.Nonce, payload.Recipient)
	// default:
	// 	err = errors.New("unknown email type")
	// }

	// if err != nil {
	// 	log.Printf("Failed to send email to %s: %v", payload.Recipient, err)
	// }
	// return err
}

// handlePushNotification 处理推送通知任务 (从 routeApiPOST.go 移动并适配)
func handlePushNotification(payloadBytes []byte) error {
	var payload PushNotificationPayload
	if err := msgpack.Unmarshal(payloadBytes, &payload); err != nil {
		return err
	}

	db := base.GetDb(false)

	var post base.Post
	if err := db.First(&post, payload.PostID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("handlePushNotification: Post %d not found, skipping.", payload.PostID)
			return nil // Post deleted, no need to push
		}
		return err
	}

	if post.DeletedAt.Valid {
		return nil // Post deleted, no need to push
	}

	var attentions []base.Attention
	err := db.Model(&base.Attention{}).Where("post_id = ?", post.ID).Find(&attentions).Error
	if err != nil {
		log.Printf("Error getting attentions for push notification: %v", err)
		return err
	}

	var replyToComment base.Comment
	replyToUserID := post.UserID
	if payload.ReplyToCommentID > 0 {
		db.Model(&base.Comment{}).Where("id = ? and post_id = ?", payload.ReplyToCommentID, post.ID).First(&replyToComment)
		replyToUserID = replyToComment.UserID
	}

	pushMessages := make([]base.PushMessage, 0, len(attentions)+1)
	if replyToUserID != payload.CommenterUserID {
		pushMessages = append(pushMessages, base.PushMessage{
			Message:   utils.TrimText(payload.CommentText, 100),
			Title:     payload.CommenterName + "回复了树洞#" + strconv.Itoa(int(post.ID)),
			PostID:    post.ID,
			CommentID: payload.CommentID,
			Type:      model.ReplyMeComment,
			UserID:    replyToUserID,
			UpdatedAt: time.Now(),
		})
	}

	for _, attention := range attentions {
		isReplyToUserInAttentions := false
		if len(pushMessages) > 0 && attention.UserID == pushMessages[0].UserID {
			isReplyToUserInAttentions = true
		}

		if isReplyToUserInAttentions {
			pushMessages[0].Type |= model.CommentInFavorited
		} else if attention.UserID != payload.CommenterUserID {
			pushMessages = append(pushMessages, base.PushMessage{
				Message:   utils.TrimText(payload.CommentText, 100),
				Title:     payload.CommenterName + "回复了树洞#" + strconv.Itoa(int(post.ID)),
				PostID:    post.ID,
				CommentID: payload.CommentID,
				Type:      model.CommentInFavorited,
				UserID:    attention.UserID,
				UpdatedAt: time.Now(),
			})
		}
	}

	err = base.PreProcessPushMessages(db, pushMessages)
	if err != nil {
		log.Printf("Error preprocessing push messages: %v", err)
		return err
	}
	base.SendToPushService(pushMessages)
	return nil
}