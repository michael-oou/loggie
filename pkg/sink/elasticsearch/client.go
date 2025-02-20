/*
Copyright 2021 Loggie Authors

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

package elasticsearch

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/loggie-io/loggie/pkg/core/api"
	"github.com/loggie-io/loggie/pkg/sink/codec"
	"github.com/loggie-io/loggie/pkg/util/runtime"
	es "github.com/olivere/elastic/v7"
	"github.com/pkg/errors"
)

type ClientSet struct {
	Version           string
	config            *Config
	cli               *es.Client
	codec             codec.Codec
	indexMatcher      [][]string
	documentIdMatcher [][]string
}

type Client interface {
	BulkCreate(content []byte, index string) error
	Stop()
}

func NewClient(config *Config, cod codec.Codec, indexMatcher [][]string, documentIdMatcher [][]string) (*ClientSet, error) {
	for i, h := range config.Hosts {
		if !strings.HasPrefix(h, "http") && !strings.HasPrefix(h, "https") {
			config.Hosts[i] = fmt.Sprintf("http://%s", h)
		}
	}
	var opts []es.ClientOptionFunc
	opts = append(opts, es.SetURL(config.Hosts...))
	if config.Sniff != nil {
		opts = append(opts, es.SetSniff(*config.Sniff))
	} else {
		// disable sniff by default
		opts = append(opts, es.SetSniff(false))
	}
	if config.Password != "" && config.UserName != "" {
		opts = append(opts, es.SetBasicAuth(config.UserName, config.Password))
	}
	if config.Schema != "" {
		opts = append(opts, es.SetScheme(config.Schema))
	}
	if config.Gzip != nil {
		opts = append(opts, es.SetGzip(*config.Gzip))
	}

	cli, err := es.NewClient(opts...)
	if err != nil {
		return nil, err
	}

	return &ClientSet{
		cli:               cli,
		config:            config,
		codec:             cod,
		indexMatcher:      indexMatcher,
		documentIdMatcher: documentIdMatcher,
	}, nil
}

func (c *ClientSet) BulkIndex(ctx context.Context, batch api.Batch) error {
	req := c.cli.Bulk()
	for _, event := range batch.Events() {
		header := event.Header()
		headerObj := runtime.NewObject(header)

		// select index
		idx, err := runtime.PatternFormat(headerObj, c.config.Index, c.indexMatcher)
		if err != nil {
			return errors.WithMessagef(err, "select index pattern error")
		}

		data, err := c.codec.Encode(event)
		if err != nil {
			return errors.WithMessagef(err, "codec encode event: %s error", event.String())
		}

		bulkIndexRequest := es.NewBulkIndexRequest().Index(idx).Doc(json.RawMessage(data))
		if len(c.config.Etype) > 0 {
			bulkIndexRequest.Type(c.config.Etype)
		}
		if c.config.DocumentId != "" {
			id, err := runtime.PatternFormat(headerObj, c.config.DocumentId, c.documentIdMatcher)
			if err != nil {
				return errors.WithMessagef(err, "format documentId %s failed", c.config.DocumentId)
			}
			bulkIndexRequest.Id(id)
		}

		req.Add(bulkIndexRequest)
	}
	ret, err := req.Do(ctx)
	if err != nil {
		return err
	}
	if ret.Errors {
		out, _ := json.Marshal(ret)
		return errors.Errorf("request to elasticsearch response error: %s", out)
	}

	return nil
}

func (c *ClientSet) Stop() {
	if c.cli != nil {
		c.cli.Stop()
	}
}
