package queue

import "treehollow-v3-backend/pkg/base"

type TaskType string

const (
	TaskSendEmail        TaskType = "email:send"
	TaskPushNotification TaskType = "notification:push"
)

// EmailPayload 定义了发送邮件任务所需的数据
type EmailPayload struct {
	Type      string // "validation", "nonce", "unregister"
	Recipient string
	Code      string // for validation
	Nonce     string // for nonce
}

// PushNotificationPayload 定义了推送通知任务所需的数据
type PushNotificationPayload struct {
	PostID           int32
	CommenterUserID  int32
	CommentID        int32
	CommentText      string
	CommenterName    string
	ReplyToCommentID int
}

// FullPushNotificationTask 包含了处理推送所需的完整上下文
// 在 worker 中从数据库获取这些信息
type FullPushNotificationTask struct {
	Payload PushNotificationPayload
	Post    base.Post
	User    base.User
}