package main

import (
	"fmt"
	"log"
	"os"
	"treehollow-v3-backend/pkg/base"
	"treehollow-v3-backend/pkg/config"
	"treehollow-v3-backend/pkg/utils"

	"github.com/google/uuid"
)

const UserCount = 1000

func main() {
	config.InitConfigFile()
	base.InitDb()
	db := base.GetDb(false)

	log.Println("Starting token generation process...")

	// --- 1. 从数据库中随机获取 1000 个用户 ---
	var users []base.User
	log.Printf("Fetching %d random users from the database...\n", UserCount)
	// 使用 Order("RAND()") 来随机排序，然后取前 UserCount 个
	// 注意: 在非常大的表上（千万级以上），RAND() 性能可能较低，但对于测试数据生成是完全可以接受的
	if err := db.Order("RAND()").Limit(UserCount).Find(&users).Error; err != nil {
		panic(fmt.Errorf("failed to fetch users: %w", err))
	}

	if len(users) < UserCount {
		log.Printf("Warning: Found only %d users, which is less than the requested %d.\n", len(users), UserCount)
		if len(users) == 0 {
			log.Println("No users found in the database. Please generate user data first.")
			return
		}
	} else {
		log.Printf("Successfully fetched %d users.\n", len(users))
	}


	// --- 2. 准备写入Token的文件 ---
	// 使用 O_TRUNC 标志来确保每次运行时都清空并重写文件，而不是追加
	logFile, err := os.OpenFile("loadtest/pressure_test_tokens.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		panic(err)
	}
	defer logFile.Close()


	// --- 3. 为每个用户生成一个新的设备和Token ---
	log.Println("Generating new devices and tokens for each user...")
	devices := make([]base.Device, 0, len(users))
	for _, user := range users {
		token := utils.GenToken()
		// 将token写入文件
		_, _ = fmt.Fprintln(logFile, token)

		// 准备要插入数据库的设备信息
		devices = append(devices, base.Device{
			ID:             uuid.New().String(),
			UserID:         user.ID,
			Token:          token,
			DeviceInfo:     "PressureTestToken",
			Type:           base.AndroidDevice, // 可以随机化设备类型
			LoginIP:        "127.0.0.1",
			LoginCity:      "Unknown",
			IOSDeviceToken: "",
		})
	}


	// --- 4. 将新设备批量写入数据库 ---
	log.Printf("Batch inserting %d new devices into the database...\n", len(devices))
	// 对于1000条记录，100的批次大小是比较合理的
	if err = db.CreateInBatches(devices, 100).Error; err != nil {
		panic(fmt.Errorf("failed to create devices in batches: %w", err))
	}

	log.Println("----------------------------------------------------")
	log.Printf("Successfully generated and saved %d tokens.\n", len(devices))
	log.Println("You can now find them in 'pressure_test_tokens.txt'.")
	log.Println("----------------------------------------------------")
}
