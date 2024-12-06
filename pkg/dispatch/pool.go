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

package dispatch

import (
	"context"
	"fmt"
)

type Pool struct {
	Num    int
	PoolCh chan struct{}
}

func NewPool(num int) *Pool {
	if num < 5 {
		num = 5
	}
	if num > 100 {
		num = 100
	}
	return &Pool{
		Num:    num,
		PoolCh: make(chan struct{}, num),
	}
}

func (p *Pool) Run(ctx context.Context, worker func(ctx context.Context) error) error {
	p.PoolCh <- struct{}{}

	errCh := make(chan error, 1)
	defer close(errCh)
	go func() {
		defer func() {
			<-p.PoolCh
		}()

		errCh <- worker(ctx)
	}()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context timeout")
		case err := <-errCh:
			return err
		}
	}
}
