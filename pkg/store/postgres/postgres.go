/*
 Copyright 2023 Friday Author.

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

package postgres

import (
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/basenana/friday/pkg/store/db"
	"github.com/basenana/friday/pkg/store/utils"
	"github.com/basenana/friday/pkg/utils/logger"
)

const defaultNamespace = "global"

type PostgresClient struct {
	log     *zap.SugaredLogger
	dEntity *db.Entity
}

func NewPostgresClient(postgresUrl string) (*PostgresClient, error) {
	log := logger.NewLog("postgres")
	dbObj, err := gorm.Open(postgres.Open(postgresUrl), &gorm.Config{Logger: utils.NewDbLogger()})
	if err != nil {
		panic(err)
	}

	dbConn, err := dbObj.DB()
	if err != nil {
		return nil, err
	}

	dbConn.SetMaxIdleConns(5)
	dbConn.SetMaxOpenConns(50)
	dbConn.SetConnMaxLifetime(time.Hour)

	if err = dbConn.Ping(); err != nil {
		return nil, err
	}

	dbEnt, err := db.NewDbEntity(dbObj, Migrate)
	if err != nil {
		return nil, err
	}

	return &PostgresClient{
		log:     log,
		dEntity: dbEnt,
	}, nil
}
