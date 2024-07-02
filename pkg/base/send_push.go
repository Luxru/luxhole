package base

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"treehollow-v3-backend/pkg/model"

	"github.com/spf13/viper"
	"gorm.io/gorm"
)

func PreProcessPushMessages(tx *gorm.DB, msgs []PushMessage) error {
	var userIDs []int32
	for _, msg := range msgs {
		userIDs = append(userIDs, msg.UserID)
	}

	var pushSettings []PushSettings
	err := tx.Model(&PushSettings{}).Where("user_id in (?)", userIDs).
		Find(&pushSettings).Error
	if err != nil {
		log.Printf("read push settings failed: %s", err)
		return err
	}

	pushSettingsMap := make(map[int32]PushSettings)
	for _, s := range pushSettings {
		pushSettingsMap[s.UserID] = s
	}

	for i, msg := range msgs {
		s, ok := pushSettingsMap[msg.UserID]
		if ok {
			if (s.Settings & msg.Type) > 0 {
				msgs[i].DoPush = true
			} else {
				msgs[i].DoPush = false
			}
		} else if (msg.Type & (model.SystemMessage | model.ReplyMeComment)) > 0 {
			msgs[i].DoPush = true
		} else {
			msgs[i].DoPush = false
		}
	}
	return nil
}

func SendToPushService(msgs []PushMessage) {
	// 过滤掉不需要推送的消息
	var messagesToPush []PushMessage
	for _, msg := range msgs {
		if msg.DoPush {
			messagesToPush = append(messagesToPush, msg)
		}
	}
	if len(messagesToPush) == 0 {
		return
	}

	// 写入数据库
	if err := db.Create(&messagesToPush).Error; err != nil {
		log.Printf("create push messages failed: %s", err)
		return
	}
	for _, msg := range msgs{
		log.Printf("Send Push msg for %d title: %s content: %s", msg.UserID, msg.Title, msg.Message)
	}
	// 发送到推送服务
	// postBody, _ := json.Marshal(messagesToPush)
	// bytesBody := bytes.NewBuffer(postBody)
	// resp, err := http.Post(
	// 	"http://"+viper.GetString("push_internal_api_listen_address")+"/send_messages",
	// 	"application/json",
	// 	bytesBody)

	// if err != nil {
	// 	log.Printf("push failed: %s\n", err)
	// 	return
	// }
	// defer resp.Body.Close()
}

func SendDeletionToPushService(commentID int32) {
	postBody, _ := json.Marshal(commentID)
	bytesBody := bytes.NewBuffer(postBody)
	req, err2 := http.NewRequest("POST",
		"http://"+viper.GetString("push_internal_api_listen_address")+"/delete_messages", bytesBody)
	if err2 != nil {
		log.Printf("push request build failed: %s\n", err2)
		return
	}
	clientHttp := &http.Client{}
	resp, err3 := clientHttp.Do(req)
	if err3 != nil {
		log.Printf("push failed: %s\n", err3)
		return
	}
	_ = resp.Body.Close()
}
