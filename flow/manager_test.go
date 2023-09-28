/*
 * Copyright 2023 friday
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

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
