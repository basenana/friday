package flow

import (
	"context"
	"log"
	"os"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("TestQuestion", func() {
	Context("do", func() {
		var (
			manager *Manager
		)
		It("init manager should be succeed", func() {
			manager = NewManager(binDir, fridayConfig)
		})
		It("create question workflow should be succeed", func() {
			question := "What is JuiceFS?"
			err := manager.Question(context.TODO(), question)
			Expect(err).Should(BeNil())
		})
	})
})

var _ = Describe("TestQuestionFlow", func() {
	Context("do", func() {
		var (
			manager *Manager
		)
		It("init manager should be succeed", func() {
			manager = NewManager(binDir, fridayConfig)
		})
		It("create question workflow should be succeed", func() {
			id := uuid.New().String()
			question := "What is JuiceFS?"

			flow, err := manager.NewQuestionFlow(id, question)
			Expect(err).Should(BeNil())
			Expect(flow).ShouldNot(BeNil())
		})
	})
})

var _ = Describe("TestIngest", func() {
	Context("do", func() {
		var (
			manager      *Manager
			knowledgeDir string
		)
		It("init manager should be succeed", func() {
			manager = NewManager(binDir, fridayConfig)
		})
		It("init a knowledge dir", func() {
			knowledgeDir = "/tmp/fridaytest.txt"
			file, err := os.Create(knowledgeDir)
			if err != nil {
				log.Fatal(err)
			}
			defer file.Close()

			_, err = file.WriteString("Hello, World!")
			if err != nil {
				log.Fatal(err)
			}

			err = file.Sync()
			if err != nil {
				log.Fatal(err)
			}
		})
		It("create ingest workflow should be succeed", func() {
			err := manager.Ingest(context.TODO(), knowledgeDir)
			Expect(err).Should(BeNil())
		})
	})
})
