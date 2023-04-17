package main

import (
	"fmt"

	"friday/pkg/friday"
	"friday/pkg/llm/prompts"
	"friday/pkg/utils/logger"
)

func main() {
	logger.InitLogger()
	defer logger.Sync()

	f, err := friday.NewFriday(&friday.Config{
		LLMType:         "openai",
		SpliterType:     "text",
		ChunkSize:       400,
		EmbeddingType:   "openai",
		VectorStoreType: "redis",
		VectorUrl:       "localhost:6379",
	})
	if err != nil {
		panic(err)
	}

	query := "如何查看 JuiceFS 的监控？"
	p := prompts.NewKnowledgePrompt()
	a, err := f.Question(p, query)
	if err != nil {
		panic(err)
	}
	fmt.Println(a)
}
