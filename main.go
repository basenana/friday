package main

import (
	"fmt"

	"friday/pkg/friday"
	"friday/pkg/llm/prompts"
)

func main() {
	f, err := friday.NewFriday(&friday.Config{
		EmbeddingType:   "openai",
		VectorStoreType: "redis",
		VectorUrl:       "localhost:6379",
		LLMType:         "openai",
	})
	if err != nil {
		panic(err)
	}

	//f.Ingest("1", "I love cats.")
	//f.Ingest("2", "I love dogs.")
	//f.Ingest("3", "Cats like eating fish.")
	//f.Ingest("4", "Dogs like eating meat.")
	//f.Ingest("5", "I do not like cats.")
	//
	query := "What is my favorite animal that eats fish?"
	p := prompts.NewKnowledgePrompt()
	a, err := f.Question(p, query)
	if err != nil {
		panic(err)
	}
	fmt.Println(a)
}
