package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"treehollow-v3-backend/pkg/base"
	"treehollow-v3-backend/pkg/config"
	"treehollow-v3-backend/pkg/consts"
	"treehollow-v3-backend/pkg/logger"
	"treehollow-v3-backend/pkg/route/auth"
	"treehollow-v3-backend/pkg/route/contents"
	"treehollow-v3-backend/pkg/route/security"
	"treehollow-v3-backend/pkg/utils"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func main() {
	logger.InitLog(consts.AllAPiLogFile)
	config.InitConfigFile()

	//if false == viper.GetBool("is_debug") {
	//	fmt.Print("Read salt from stdin: ")
	//	_, _ = fmt.Scanln(&utils.Salt)
	//	if utils.SHA256(utils.Salt) != viper.GetString("salt_hashed") {
	//		panic("salt verification failed!")
	//	}
	//}
	utils.Salt = viper.GetString("salt")

	base.InitDb()
	base.AutoMigrateDb()

	utils.InitGeoDbRefreshCron()

	log.Println("start time: ", time.Now().Format("01-02 15:04:05"))
	if false == viper.GetBool("is_debug") {
		gin.SetMode(gin.ReleaseMode)
	}

	contents.RefreshHotPosts()
	contents.InitHotPostsRefreshCron()
	contents.InitService()
	
	r := gin.New()
	corsConfig := cors.DefaultConfig()
	corsConfig.AllowAllOrigins = true
	corsConfig.AllowHeaders = append(corsConfig.AllowHeaders, "TOKEN")
	r.Use(cors.New(corsConfig))
	r.Use(gin.Logger(), gin.Recovery(), cors.New(corsConfig), auth.AuthMiddleware())
	security.AddSecurityControllers(r)
	contents.AddContentsControllers(r)
	r.Static("/images", "./images")

	listenAddr := viper.GetString("all_api_listen_address")
	if strings.Contains(listenAddr, ":") {
		_ = r.Run(listenAddr)
	} else {
		_ = os.MkdirAll(filepath.Dir(listenAddr), os.ModePerm)
		_ = os.Remove(listenAddr)

		listener, err := net.Listen("unix", listenAddr)
		utils.FatalErrorHandle(&err, "bind failed")
		log.Printf("Listening and serving HTTP on unix: %s.\n"+
			"Note: 0777 is not a safe permission for the unix socket file. "+
			"It would be better if the user manually set the permission after startup\n",
			listenAddr)
		_ = os.Chmod(listenAddr, 0777)
		err = http.Serve(listener, r)
		return
	}
}
