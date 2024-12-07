/*
 Copyright 2024 Friday Author.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/basenana/friday/config"
	"github.com/basenana/friday/pkg/service"
	"github.com/basenana/friday/pkg/utils/logger"
)

const (
	defaultHttpTimeout = time.Minute * 30
)

type HttpServer struct {
	log    *zap.SugaredLogger
	engine *gin.Engine
	chain  *service.Chain
	addr   string
}

func NewHttpServer(conf config.Config) (*HttpServer, error) {
	chain, err := service.NewChain(conf)
	if err != nil {
		return nil, err
	}
	return &HttpServer{
		log:    logger.NewLog("api"),
		engine: gin.Default(),
		chain:  chain,
		addr:   conf.HttpAddr,
	}, nil
}

func (s *HttpServer) Run(stopCh chan struct{}) {
	httpServer := &http.Server{
		Addr:         s.addr,
		Handler:      s.engine,
		ReadTimeout:  defaultHttpTimeout,
		WriteTimeout: defaultHttpTimeout,
	}
	s.handle(s.engine.Group("/api"))

	go func() {
		if err := httpServer.ListenAndServe(); err != nil {
			if err != http.ErrServerClosed {
				s.log.Panicw("api server down", "err", err)
			}
			s.log.Info("api server stopped")
		}
	}()

	<-stopCh
	shutdownCtx, canF := context.WithTimeout(context.TODO(), time.Second)
	defer canF()
	_ = httpServer.Shutdown(shutdownCtx)
}

func (s *HttpServer) ping(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "pong"})
}

func (s *HttpServer) handle(group *gin.RouterGroup) {
	group.GET("/ping", s.ping)
	docGroup := group.Group("/namespace/:namespace/docs")
	docGroup.POST("/entry/:entryId", s.store())
	docGroup.DELETE("/entry/:entryId", s.delete())
	docGroup.PUT("/entry/:entryId", s.update())
	docGroup.GET("/search", s.search())
}
