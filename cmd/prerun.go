package cmd

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"time"
	"ttsapi/config"
	"ttsapi/logger"
	"ttsapi/storage/gorm"
	rds "ttsapi/storage/redis"
	"ttsapi/utils/exit"
)

func init() {
	viper.SetConfigName("config")
	viper.AddConfigPath("/etc/config/")
	viper.AddConfigPath("./config")
}

func preRun(cmd *cobra.Command, args []string) {
	configFile, _ := cmd.Flags().GetString("config")
	logger.Infof(context.Background(), "cmdline config file: %v", configFile)
	if _, err := config.Load(configFile); err != nil {
		panic(fmt.Errorf("config load failed: %s", err.Error()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conf := config.Get()
	// TODO Mongo初始化
	if conf.Resources != nil && len(conf.Resources.Storage.Mysql) > 0 {
		if err := gorm.Init(ctx, conf.Resources.Storage.Mysql, gorm.DBTypeMysql); err != nil {
			panic(fmt.Errorf("MySQL init error: %s", err.Error()))
		}
	}
	if conf.Resources != nil && len(conf.Resources.Storage.Postgresql) > 0 {
		if err := gorm.Init(ctx, conf.Resources.Storage.Postgresql, gorm.DBTypePostgresql); err != nil {
			panic(fmt.Errorf("PostgreSQL init error: %s", err.Error()))
		}
	}

	if conf.Resources != nil && len(conf.Resources.Storage.Redis) > 0 {
		if err := rds.Init(ctx, conf.Resources.Storage.Redis); err != nil {
			panic(fmt.Errorf("redis init error: %s", err.Error()))
		}
	}
	go exit.HouseKeeping()
}
