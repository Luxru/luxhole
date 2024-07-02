package contents

import (
	"context"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/spf13/viper"
	"log"
	"strconv"
	"treehollow-v3-backend/pkg/base"

	"github.com/go-redis/redis/v8"
)


// GetHotPostIDsFromCache 从 Redis ZSET 中获取热榜帖子ID
func GetHotPostIDsFromCache(page, pageSize int) ([]int32, error) {
	client := base.GetRedisClient()
	start := int64((page - 1) * pageSize)
	stop := start + int64(pageSize) - 1

	// 从 ZSET 中按分值从高到低获取成员（帖子ID）
	idStrs, err := client.ZRevRange(context.Background(), base.HotListKey, start, stop).Result()
	if err != nil {
		return nil, err
	}

	ids := make([]int32, len(idStrs))
	for i, s := range idStrs {
		id, _ := strconv.Atoi(s)
		ids[i] = int32(id)
	}
	return ids, nil
}

// ColdStartHotList 冷启动热榜，从数据库读取数据填充到 Redis ZSET
func ColdStartHotList() {
	log.Println("Performing cold start for hot list...")
	avg, err := load.Avg()
	if err != nil {
		log.Printf("Cold start failed: could not get system load: %v", err)
		return
	}
	if avg.Load1 > viper.GetFloat64("sys_load_threshold") {
		log.Println("Cold start skipped: system load is too high.")
		return
	}

	// 使用原始的数据库查询逻辑
	hotPosts, err := base.GetHotPosts()
	if err != nil {
		log.Printf("Cold start failed: db.GetHotPosts() failed: err=%s\n", err)
		return
	}

	client := base.GetRedisClient()
	pipe := client.Pipeline()
	// 清理旧的热榜，以防万一
	pipe.Del(context.Background(), base.HotListKey)

	// 将查询到的帖子批量加入ZSET
	for _, post := range hotPosts {
		score := base.ComputeScore(&post)
		pipe.ZAdd(context.Background(), base.HotListKey, &redis.Z{
			Score:  score,
			Member: strconv.Itoa(int(post.ID)),
		})
	}

	if _, err := pipe.Exec(context.Background()); err != nil {
		log.Printf("Cold start failed: redis pipeline failed: %v", err)
	} else {
		log.Printf("Cold start finished. Loaded %d posts into hot list.", len(hotPosts))
	}
}

func InitHotPostsRefreshCron() {
	// 服务启动时执行一次冷启动
	go ColdStartHotList()
	// 不再需要 cron 定时任务，因为热榜是实时更新的
}