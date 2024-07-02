package queue

import (
	"context"
	"github.com/vmihailenco/msgpack/v5"
	"time"
	"treehollow-v3-backend/pkg/base"

	"github.com/go-redis/redis/v8"
)

const (
	DefaultQueue = "webhole:queue:default"
	DelayedQueue = "webhole:queue:delayed"
)

type Task struct {
	Type    TaskType
	Payload []byte
}

// Enqueue 将任务添加到默认队列，立即执行
func Enqueue(taskType TaskType, payload interface{}) error {
	payloadBytes, err := msgpack.Marshal(payload)
	if err != nil {
		return err
	}

	task := Task{
		Type:    taskType,
		Payload: payloadBytes,
	}

	taskBytes, err := msgpack.Marshal(task)
	if err != nil {
		return err
	}

	client := base.GetRedisClient()
	return client.LPush(context.Background(), DefaultQueue, taskBytes).Err()
}

// EnqueueWithDelay 将任务添加到延迟队列，在指定时间后执行
func EnqueueWithDelay(delay time.Duration, taskType TaskType, payload interface{}) error {
	payloadBytes, err := msgpack.Marshal(payload)
	if err != nil {
		return err
	}

	task := Task{
		Type:    taskType,
		Payload: payloadBytes,
	}

	taskBytes, err := msgpack.Marshal(task)
	if err != nil {
		return err
	}

	client := base.GetRedisClient()
	score := float64(time.Now().Add(delay).Unix())
	return client.ZAdd(context.Background(), DelayedQueue, &redis.Z{
		Score:  score,
		Member: taskBytes,
	}).Err()
}