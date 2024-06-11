package main

import (
	"fmt"
	"log"
	"math/rand"
	"treehollow-v3-backend/pkg/base"
	"treehollow-v3-backend/pkg/config"
	"treehollow-v3-backend/pkg/consts"
	"treehollow-v3-backend/pkg/utils"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

const (
	UserCount    = 10000  // 创建 1万 用户
	PostCount    = 10000  // 创建 1万 帖子
	CommentCount = 100000 // 创建 10万 评论
	BatchSize    = 1000   // 每批次插入数量
)

func main() {
	config.InitConfigFile()
	utils.Salt = viper.GetString("salt")
	base.InitDb()
	log.Println("Database connection initialized.")

	err := base.GetDb(false).Transaction(func(tx *gorm.DB) error {
		log.Println("Starting data generation...")

		log.Printf("Creating %d users...\n", UserCount)
		users := make([]base.User, 0, UserCount)
		passwd := "11111111"
		passwd_hash := utils.SHA256(utils.SHA256(passwd))
		for i := 0; i < UserCount; i++ {
			email := gofakeit.Email()
			email_encrypted, err := utils.AESEncrypt(email, passwd_hash)
			if err != nil {
				panic(err)
			}
			users = append(users, base.User{
				EmailEncrypted: email_encrypted,
				ForgetPwNonce:  utils.GenNonce(),
				Role:           base.NormalUserRole,
			})
		}
		if err := tx.CreateInBatches(users, BatchSize).Error; err != nil {
			return fmt.Errorf("failed to create users: %w", err)
		}
		log.Println("Users created successfully.")

		log.Printf("Creating %d posts...\n", PostCount)
		posts := make([]base.Post, 0, PostCount)
		for i := 0; i < PostCount; i++ {
			posts = append(posts, base.Post{
				UserID:       int32(rand.Intn(UserCount)) + 1,
				Text:         gofakeit.Sentence(30),
				Type:         "text",
				Tag:          gofakeit.RandomString([]string{"", "生活", "学习", "工作"}),
				FilePath:     "",
				FileMetadata: "{}",
			})
		}
		if err := tx.CreateInBatches(posts, BatchSize).Error; err != nil {
			return fmt.Errorf("failed to create posts: %w", err)
		}
		log.Println("Posts created successfully.")

		log.Printf("Creating %d comments...\n", CommentCount)
		var allPosts []struct {
			ID     int32
			UserID int32
		}
		if err := tx.Model(&base.Post{}).Select("id", "user_id").Find(&allPosts).Error; err != nil {
			return fmt.Errorf("failed to fetch posts for comments: %w", err)
		}
		if len(allPosts) == 0 {
			log.Println("No posts found, skipping comment creation.")
			return nil
		}

		nameCache := make(map[int32]map[int32]string)
		postCommenterCount := make(map[int32]int)
		postCommentersToCreate := make([]base.PostCommenter, 0, CommentCount)

		comments := make([]base.Comment, 0, BatchSize)

		log.Println("Generating and inserting comments in batches...")
		for i := 0; i < CommentCount; i++ {
			post := allPosts[rand.Intn(len(allPosts))]
			commenterID := int32(rand.Intn(UserCount)) + 1
			var name string

			if post.UserID == commenterID {
				name = consts.DzName
			} else {
				if postCache, ok := nameCache[post.ID]; ok {
					if cachedName, ok := postCache[commenterID]; ok {
						name = cachedName
					}
				}
				if name == "" {
					currentCount := postCommenterCount[post.ID]
					newCount := currentCount + 1
					postCommenterCount[post.ID] = newCount

					newName := utils.GetCommenterName(newCount, consts.Names0, consts.Names1)
					name = newName

					if _, ok := nameCache[post.ID]; !ok {
						nameCache[post.ID] = make(map[int32]string)
					}
					nameCache[post.ID][commenterID] = newName

					postCommentersToCreate = append(postCommentersToCreate, base.PostCommenter{
						PostID:        post.ID,
						UserID:        commenterID,
						CommenterName: newName,
					})
				}
			}

			comments = append(comments, base.Comment{
				PostID:       post.ID,
				UserID:       commenterID,
				Text:         gofakeit.Sentence(20),
				Type:         "text",
				Name:         name,
				FilePath:     "",
				FileMetadata: "{}",
			})

			if (i+1)%BatchSize == 0 || i == CommentCount-1 {
				if err := tx.CreateInBatches(comments, BatchSize).Error; err != nil {
					return fmt.Errorf("failed to create comments batch: %w", err)
				}
				log.Printf("Inserted a batch of %d comments, total progress %d/%d\n", len(comments), i+1, CommentCount)

				comments = comments[:0]
			}
		}
		log.Println("All comment batches created successfully.")

		log.Println("Batch inserting post commenter mappings...")
		if len(postCommentersToCreate) > 0 {
			if err := tx.CreateInBatches(postCommentersToCreate, BatchSize).Error; err != nil {
				return fmt.Errorf("failed to create post commenter mappings: %w", err)
			}
		}
		log.Println("Post commenter mappings created successfully.")

		log.Println("Updating distinct commenter counts in posts...")
		for postID, count := range postCommenterCount {
			if err := tx.Model(&base.Post{}).Where("id = ?", postID).Update("distinct_commenter_count", count).Error; err != nil {
				return fmt.Errorf("failed to update distinct commenter count for post %d: %w", postID, err)
			}
		}
		log.Println("Distinct commenter counts updated successfully.")
		return nil
	})

	if err != nil {
		log.Fatalf("Data generation failed: %v", err)
	}
	log.Println("Data generation finished successfully!")
}