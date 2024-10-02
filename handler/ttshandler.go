package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gomodule/redigo/redis"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
	"ttsapi/config"
	"ttsapi/logger"
	"ttsapi/server/httpserver"
	"ttsapi/server/httpserver/middles/status"
	rds "ttsapi/storage/redis"
	"unicode"
)

const taskList = "ttsapi:tasks"

type TTShHandler struct {
	ttsAddr           string
	gptWeightsPath    string
	soVITSWeightsPath string
	weightPairs       map[string]pair
	referAudioPath    string
	outputAudioPath   string
	currentModel      string
	mutex             sync.Mutex
	base
}

func (handler *TTShHandler) Init(router *gin.RouterGroup) {
	handler.logger = logger.WithField("handler", "TTShHandler")

	cfg := config.Get()
	handler.ttsAddr = cfg.Server.TTSAddress
	handler.gptWeightsPath = cfg.Server.GPTWeightsPath
	handler.soVITSWeightsPath = cfg.Server.SoVITSWeightsPath
	handler.referAudioPath = cfg.Server.ReferAudioPath
	handler.outputAudioPath = cfg.Server.OutputAudioPath
	handler.loadModels()

	go func() {
		ctx := context.Background()
		logger.Infof(ctx, "task prossor started")
		for {
			if err := handler.process(ctx); err != nil {
				logger.Errorf(ctx, "Task process err: %s", err)
				time.Sleep(5 * time.Second)
			}
		}
	}()

	if router != nil {
		router.Use(func(ctx *gin.Context) {
			authorization := ctx.Request.Header.Get("Authorization")
			if authorization == "" || authorization != config.Get().Server.Authorization {
				ctx.AbortWithStatusJSON(
					http.StatusUnauthorized,
					gin.H{"error": "Unauthorized"},
				)
			}
			ctx.Next()
		})

		router.GET("/getModels", httpserver.NewHandlerFuncFrom(handler.GetModels))
		router.GET("/loadModels", httpserver.NewHandlerFuncFrom(handler.LoadModels))
		router.POST("/newTask", httpserver.NewHandlerFuncFrom(handler.NewTask))
		router.GET("/taskStatus", httpserver.NewHandlerFuncFrom(handler.TaskStatus))
		router.GET("/getResult", handler.GetResult)
	}
}

func (handler *TTShHandler) loadModels() {
	models := make(map[string]pair)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	gptWeights, err := os.ReadDir(handler.gptWeightsPath)
	if err != nil {
		logger.Warnf(ctx, "Error reading gpt weights directory %s", handler.gptWeightsPath)
		return
	}

	referenceAudios, err := os.ReadDir(handler.referAudioPath)
	if err != nil {
		logger.Warnf(ctx, "Error reading reference audios directory %s", handler.referAudioPath)
		return
	}

	audios := make(map[string][]string)
	for _, audio := range referenceAudios {
		fileName := audio.Name()
		name := fileName[0 : len(fileName)-len(path.Ext(fileName))]
		res := strings.Split(name, "-")
		if len(res) != 3 {
			logger.Warnf(ctx, "Invalid reference audio %s", fileName)
			continue
		}
		audios[res[0]] = []string{res[1], res[2]}
	}

	for _, weight := range gptWeights {
		fileName := weight.Name()
		ext := path.Ext(fileName)
		name := fileName[0 : len(fileName)-len(ext)]
		if _, err := os.Stat(fmt.Sprintf("%s/%s.pth", handler.soVITSWeightsPath, name)); err != nil {
			logger.Warnf(ctx, "Error reading file %s", handler.soVITSWeightsPath)
			continue
		}

		referInfo, ok := audios[name]
		if !ok {
			logger.Warnf(ctx, "Error get reference audio %s", name)
			continue
		}

		models[name] = pair{
			Name:               name,
			GptPath:            handler.gptWeightsPath + "/" + fileName,
			SovitsPath:         handler.soVITSWeightsPath + "/" + name + ".pth",
			ReferenceAudioPath: fmt.Sprintf("%s/%s-%s-%s.wav", handler.referAudioPath, name, referInfo[0], referInfo[1]),
			ReferText:          referInfo[0],
			ReferLang:          referInfo[1],
		}
	}
	handler.weightPairs = models
	logger.Infof(ctx, "Loaded %d models", len(models))
}

type task struct {
	Id      string `json:"id"`
	Model   pair   `json:"model"`
	Content string `json:"content"`
	Lang    string `json:"lang"`
}

func (handler *TTShHandler) setModels(model pair) error {
	baseUrl, _ := url.Parse(handler.ttsAddr)
	baseUrl.Path = "set_gpt_weights"
	query := url.Values{}
	query.Add("weights_path", model.GptPath)
	baseUrl.RawQuery = query.Encode()
	request, err := http.NewRequest(http.MethodGet, baseUrl.String(), nil)
	if err != nil {
		return err
	}
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("set gpt model failed: " + res.Status)
	}

	baseUrl, _ = url.Parse(handler.ttsAddr)
	baseUrl.Path = "set_sovits_weights"
	query = url.Values{}
	query.Add("weights_path", model.SovitsPath)
	baseUrl.RawQuery = query.Encode()
	request, err = http.NewRequest(http.MethodGet, baseUrl.String(), nil)
	if err != nil {
		return err
	}
	res1, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusOK {
		return errors.New("set gpt model failed: " + res1.Status)
	}
	logger.Infof(context.Background(), "model changed to %s", model.Name)
	return nil
}

func (handler *TTShHandler) process(ctx context.Context) error {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	conn := rds.Get()
	defer conn.Close()
	arr, err := redis.Values(conn.Do("BRPOP", taskList, 5))
	if err != nil {
		if errors.Is(err, redis.ErrNil) {
			return nil
		}
		return err
	}
	if len(arr) != 2 {
		return errors.New("bad values")
	}
	data, ok := arr[1].([]byte)
	if !ok {
		return errors.New("bad values")
	}
	t := &task{}
	if err := json.Unmarshal(data, t); err != nil {
		return err
	}

	logger.Infof(ctx, "now handling task %v", t.Content)

	if t.Model.Name != handler.currentModel {
		err := handler.setModels(t.Model)
		if err != nil {
			return err
		}
		handler.currentModel = t.Model.Name
	}

	reqBody, err := json.Marshal(map[string]interface{}{
		"text":                t.Content,
		"text_lang":           t.Lang,
		"ref_audio_path":      t.Model.ReferenceAudioPath,
		"aux_ref_audio_paths": []string{},
		"prompt_text":         t.Model.ReferText,
		"prompt_lang":         t.Model.ReferLang,
		"top_k":               5,
		"top_p":               1,
		"temperature":         1,
		"text_split_method":   "cut0",
		"batch_size":          1,
		"batch_threshold":     0.75,
		"split_bucket":        true,
		"return_fragment":     false,
		"speed_factor":        1.0,
		"streaming_mode":      false,
		"seed":                -1,
		"parallel_infer":      true,
		"repetition_penalty":  1.35,
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tts", handler.ttsAddr), bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 300 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		resBody, _ := io.ReadAll(resp.Body)
		logger.Errorf(ctx, "response status code %d body %s", resp.StatusCode, string(resBody))
		return errors.New("bad status code " + resp.Status)
	}
	if respBody, err := io.ReadAll(resp.Body); err == nil {
		filePath := fmt.Sprintf("%s/%d-%s.wav", handler.outputAudioPath, time.Now().Unix(), t.Model.Name)
		file, err := os.Create(filePath)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := file.Write(respBody); err != nil {
			return err
		}
		rds.SetString(ctx, t.Id, filePath)
	} else {
		return err
	}
	logger.Infof(ctx, " handling task %v finished", t.Content)
	return nil
}

type pair struct {
	Name               string `json:"name"`
	GptPath            string `json:"gptPath"`
	SovitsPath         string `json:"sovitsPath"`
	ReferenceAudioPath string `json:"referenceAudioPath"`
	ReferText          string `json:"referText"`
	ReferLang          string `json:"referLang"`
}

type ModelResp struct {
	Models map[string]pair `json:"models"`
}

func (handler *TTShHandler) GetModels(ctx context.Context, req *struct{}) (*ModelResp, error) {
	return &ModelResp{Models: handler.weightPairs}, nil
}

func (handler *TTShHandler) LoadModels(ctx context.Context, req *struct{}) (*ModelResp, error) {
	handler.loadModels()
	return &ModelResp{Models: handler.weightPairs}, nil
}

type NewTaskReq struct {
	Model string `json:"model"`
	Text  string `json:"text"`
}

type NewTaskResp struct {
	Id string `json:"id"`
}

func (handler *TTShHandler) NewTask(ctx context.Context, req *NewTaskReq) (rsp *NewTaskResp, err error) {
	content := req.Text
	hasEn, hasJa := false, false
	for _, c := range content {
		if unicode.Is(unicode.Hiragana, c) || unicode.Is(unicode.Katakana, c) {
			hasJa = true
		}
		if unicode.IsLetter(c) {
			hasEn = true
		}
		if hasEn && hasJa {
			break
		}
	}

	id := uuid.New().String()
	model, ok := handler.weightPairs[req.Model]
	if !ok {
		return nil, &status.Status{
			Code:    400,
			Message: "model not found",
		}
	}
	js, err := json.Marshal(task{
		Id:      id,
		Model:   model,
		Content: content,
		Lang: func() string {
			if hasEn && !hasJa {
				return "zh" // 中英混合
			}
			if !hasEn && hasJa {
				return "all_ja" // 全日文
			}
			if hasEn && hasJa {
				return "auto" // 自动识别
			}
			return "all_zh" // 全中文
		}(),
	})
	if err != nil {
		return nil, &status.Status{
			Code:    500,
			Message: err.Error(),
		}
	}
	conn := rds.Get()
	_, err = conn.Do("LPUSH", taskList, js)
	if err != nil {
		return nil, &status.Status{
			Code:    500,
			Message: err.Error(),
		}
	}
	return &NewTaskResp{
		Id: id,
	}, nil
}

type TaskStatusReq struct {
	Id string `json:"id"`
}

type TaskStatusResp struct {
	Status string `json:"status"`
}

func (handler *TTShHandler) TaskStatus(ctx context.Context, req *TaskStatusReq) (*TaskStatusResp, error) {
	_, err := rds.GetString(ctx, req.Id)
	if err != nil {
		return nil, &status.Status{
			Code:    500,
			Message: "not finished",
		}
	}
	return nil, &status.Status{Code: 200, Message: "finished"}
}

func (handler *TTShHandler) GetResult(ctx *gin.Context) {
	id := ctx.Query("id")
	file, err := rds.GetString(ctx, id)
	if err != nil {
		ctx.JSON(500,
			gin.H{
				"code":    500,
				"message": "wrong id",
			})
		return
	}
	_, err = os.Stat(file)
	if err != nil {
		ctx.JSON(500,
			gin.H{
				"code":    500,
				"message": "open file error",
			})
		return
	}
	ctx.File(file)
	return
}
