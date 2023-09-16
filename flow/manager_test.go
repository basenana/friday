package flow

import (
	"context"
	"log"
	"os"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("TestIngest", func() {
	Context("do", func() {
		var (
			manager      *Manager
			knowledgeDir string
		)
		It("init manager should be succeed", func() {
			manager = NewManager(binDir)
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
