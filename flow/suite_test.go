package flow

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"friday/config"
	"friday/pkg/utils/logger"
)

var (
	fridayConfig config.Config
	binDir       string
)

func TestFlow(t *testing.T) {
	logger.InitLogger()
	defer logger.Sync()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Manager Suite")
}

var _ = BeforeSuite(func() {
	fridayConfig = config.Config{
		EmbeddingType:    config.EmbeddingOpenAI,
		LLMType:          config.LLMOpenAI,
		VectorStoreType:  config.VectorStoreRedis,
		VectorUrl:        "192.168.124.37:30684",
		SpliterChunkSize: 500,
	}
	binDir = "/Users/weiwei/tmp/friday"
})

var _ = AfterSuite(func() {})
