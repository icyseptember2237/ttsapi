package config

import "ttsapi/storage"

type Resource struct {
	Storage *storage.Storage `mapstructure:"storage"`
	Queue   *Queue           `mapstructure:"queue"`
}

type Queue struct {
}
