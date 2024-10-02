package config

import "ttsapi/logger"

type Server struct {
	Port              string          `mapstructure:"port"`
	TTSAddress        string          `mapstructure:"tts_address"`
	GPTWeightsPath    string          `mapstructure:"gpt_weights_path"`
	SoVITSWeightsPath string          `mapstructure:"sovits_weights_path"`
	ReferAudioPath    string          `mapstructure:"refer_audio_path"`
	OutputAudioPath   string          `mapstructure:"output_audio_path"`
	Authorization     string          `mapstructure:"authorization"`
	Log               *logger.Options `mapstructure:"log"`
}
