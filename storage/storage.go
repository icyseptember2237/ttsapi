package storage

type Storage struct {
	Mongo      map[string]string `mapstructure:"mongo,omitempty"`
	Mysql      map[string]string `mapstructure:"mysql,omitempty"`
	Postgresql map[string]string `mapstructure:"postgresql,omitempty"`
	Redis      string            `mapstructure:"redis,omitempty"`
}
