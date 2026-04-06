package setup

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/core/providers"
	"github.com/basenana/friday/core/providers/anthropics"
	"github.com/basenana/friday/core/providers/openai"
	"github.com/basenana/friday/core/types"
)

const imageToolSystemPrompt = "You are an image understanding assistant. Analyze the provided image carefully and answer the user's prompt accurately and concisely."

type imageAnalyzer struct {
	cfg     *config.Config
	mu      sync.Mutex
	clients map[string]providers.Client
}

func newImageAnalyzer(cfg *config.Config) *imageAnalyzer {
	return &imageAnalyzer{
		cfg:     cfg,
		clients: make(map[string]providers.Client),
	}
}

func (a *imageAnalyzer) Analyze(ctx context.Context, prompt, modelOverride string, image *types.ImageContent) (string, error) {
	modelCfg, err := a.cfg.ResolveImageModel(modelOverride)
	if err != nil {
		return "", err
	}

	client, err := a.clientForModel(modelCfg)
	if err != nil {
		return "", err
	}

	req := providers.NewRequest(
		imageToolSystemPrompt,
		types.Message{Role: types.RoleUser, Content: prompt, Image: image},
	)

	stream := client.Completion(ctx, req)
	return readAllProviderContent(ctx, stream)
}

func (a *imageAnalyzer) clientForModel(modelCfg config.ModelConfig) (providers.Client, error) {
	key := fmt.Sprintf("%s|%s|%s|%s|%d|%g|%d|%s",
		modelCfg.Provider,
		modelCfg.BaseURL,
		modelCfg.Key,
		modelCfg.Model,
		modelCfg.MaxTokens,
		modelCfg.Temperature,
		modelCfg.QPM,
		modelCfg.Proxy,
	)

	a.mu.Lock()
	defer a.mu.Unlock()

	if client, ok := a.clients[key]; ok {
		return client, nil
	}

	client, err := CreateProviderClientFromModel(modelCfg)
	if err != nil {
		return nil, err
	}
	a.clients[key] = client
	return client, nil
}

func CreateProviderClient(cfg *config.Config) (providers.Client, error) {
	return CreateProviderClientFromModel(cfg.Model)
}

func CreateProviderClientFromModel(modelCfg config.ModelConfig) (providers.Client, error) {
	provider := strings.ToLower(modelCfg.Provider)

	switch provider {
	case "anthropic":
		host := modelCfg.BaseURL
		if host == "" {
			host = "https://api.anthropic.com"
		}
		temp := modelCfg.Temperature
		maxTokens := int64(modelCfg.MaxTokens)
		return anthropics.New(host, modelCfg.Key, anthropics.Model{
			Name:        modelCfg.Model,
			Temperature: &temp,
			MaxTokens:   &maxTokens,
			QPM:         modelCfg.QPM,
			Proxy:       modelCfg.Proxy,
		}), nil
	case "openai", "":
		host := modelCfg.BaseURL
		if host == "" {
			host = "https://api.openai.com/v1"
		}
		temp := modelCfg.Temperature
		return openai.New(host, modelCfg.Key, openai.Model{
			Name:        modelCfg.Model,
			Temperature: &temp,
			QPM:         modelCfg.QPM,
			Proxy:       modelCfg.Proxy,
		}), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", modelCfg.Provider)
	}
}

func readAllProviderContent(ctx context.Context, resp providers.Response) (string, error) {
	var (
		contentBuf = &bytes.Buffer{}
		err        error
	)

Waiting:
	for {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			break Waiting
		case err = <-resp.Error():
			if err != nil {
				break Waiting
			}
		case delta, ok := <-resp.Message():
			if !ok {
				break Waiting
			}
			if delta.Content != "" {
				contentBuf.WriteString(delta.Content)
			}
		}
	}

	return strings.TrimSpace(contentBuf.String()), err
}
