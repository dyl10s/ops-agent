// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package apps

import (
	"context"
	"fmt"

	"github.com/GoogleCloudPlatform/ops-agent/confgenerator"
	"github.com/GoogleCloudPlatform/ops-agent/confgenerator/fluentbit"
	"github.com/GoogleCloudPlatform/ops-agent/confgenerator/otel"
	"github.com/GoogleCloudPlatform/ops-agent/internal/secret"
)

type MetricsReceiverCouchdb struct {
	confgenerator.ConfigComponent `yaml:",inline"`

	confgenerator.MetricsReceiverShared `yaml:",inline"`

	Endpoint string        `yaml:"endpoint" validate:"omitempty,url,startswith=http:"`
	Username string        `yaml:"username" validate:"required_with=Password"`
	Password secret.String `yaml:"password" validate:"required_with=Username"`
}

const defaultCouchdbEndpoint = "http://localhost:5984"

func (MetricsReceiverCouchdb) Type() string {
	return "couchdb"
}

func (r MetricsReceiverCouchdb) Pipelines(_ context.Context) ([]otel.ReceiverPipeline, error) {
	if r.Endpoint == "" {
		r.Endpoint = defaultCouchdbEndpoint
	}
	return []otel.ReceiverPipeline{{
		Receiver: otel.Component{
			Type: "couchdb",
			Config: map[string]interface{}{
				"collection_interval": r.CollectionIntervalString(),
				"endpoint":            r.Endpoint,
				"username":            r.Username,
				"password":            r.Password.SecretValue(),
			},
		},
		Processors: map[string][]otel.Component{"metrics": {
			otel.NormalizeSums(),
			otel.MetricsTransform(
				otel.AddPrefix("workload.googleapis.com"),
			),
			otel.TransformationMetrics(
				otel.SetScopeName("agent.googleapis.com/"+r.Type()),
				otel.SetScopeVersion("1.0"),
			),
		}},
	}}, nil
}

func init() {
	confgenerator.MetricsReceiverTypes.RegisterType(func() confgenerator.MetricsReceiver { return &MetricsReceiverCouchdb{} })
}

type LoggingProcessorCouchdb struct {
	confgenerator.ConfigComponent `yaml:",inline"`
}

func (LoggingProcessorCouchdb) Type() string {
	return "couchdb"
}

func (p LoggingProcessorCouchdb) Components(ctx context.Context, tag string, uid string) []fluentbit.Component {
	c := confgenerator.LoggingProcessorParseMultilineRegex{
		LoggingProcessorParseRegexComplex: confgenerator.LoggingProcessorParseRegexComplex{
			Parsers: []confgenerator.RegexParser{
				{
					// Format https://github.com/apache/couchdb/blob/main/src/couch_log/src/couch_log_writer_syslog.erl#L72
					// Sample line: [notice] 2021-12-02T23:36:42.555157Z nonode@nohost <0.17165.1> a5f585a0d3 localhost:5984 127.0.0.1 otelu PUT /oteld 201 ok 16
					Regex: `^\[(?<level>\w*)\] (?<timestamp>[\d\-\.:TZ]+) (?<node>\S+)@(?<host>[^\s]+) \<(?<pid>[^ ]*)\> [\w-]+ (?<http_request_serverIp>[^ ]*) (?<http_request_remoteIp>[^ ]*) (?<message>(?<remote_user>[^ ]*) (?<http_request_requestMethod>[^ ]*) (?<path>[^ ]*) (?<http_request_status>[^ ]*) (?<status_message>[^ ]*) (?<http_request_responseSize>[\d]*)$)`,
					Parser: confgenerator.ParserShared{
						TimeKey:    "timestamp",
						TimeFormat: "%Y-%m-%dT%H:%M:%S.%L%z",
						Types: map[string]string{
							"http_request_status": "integer",
						},
					},
				},
				{
					/*  Format https://github.com/apache/couchdb/blob/main/src/couch_log/src/couch_log_writer_syslog.erl#L72
					Sample line1: [info] 2022-01-12T16:52:56.998128Z nonode@nohost <0.216.0> -------- Apache CouchDB has started. Time to relax.
					Sample line2:
					[error] 2022-01-12T16:53:03.094488Z nonode@nohost emulator -------- Error in process <0.463.0> with exit value:
					{database_does_not_exist,[{mem3_shards,load_shards_from_db,"_users",[{file,"src/mem3_shards.erl"},{line,399}]},{mem3_shards,load_shards_from_disk,1,[{file,"src/mem3_shards.erl"},{line,374}]},{mem3_shards,load_shards_from_disk,2,[{file,"src/mem3_shards.erl"},{line,403}]},{mem3_shards,for_docid,3,[{file,"src/mem3_shards.erl"},{line,96}]},{fabric_doc_open,go,3,[{file,"src/fabric_doc_open.erl"},{line,39}]},{chttpd_auth_cache,ensure_auth_ddoc_exists,2,[{file,"src/chttpd_auth_cache.erl"},{line,198}]},{chttpd_auth_cache,listen_for_changes,1,[{file,"src/chttpd_auth_cache.erl"},{line,145}]}]}
					*/
					Regex: `^\[(?<level>\w*)\] (?<timestamp>[\d\-\.:TZ]+) (?<node>\S+)@(?<host>[^\s]+) (?<message>[\s\S]*(\<(?<pid>[^>]+)\>)[\s\S]*)`,
					Parser: confgenerator.ParserShared{
						TimeKey:    "timestamp",
						TimeFormat: "%Y-%m-%dT%H:%M:%S.%L%z",
					},
				},
			},
		},
	}.Components(ctx, tag, uid)

	fields := map[string]*confgenerator.ModifyField{
		"severity": {
			CopyFrom: "jsonPayload.level",
			MapValues: map[string]string{
				"emerg":     "EMERGENCY",
				"emergency": "EMERGENCY",
				"alert":     "ALERT",
				"crit":      "CRITICAL",
				"critical":  "CRITICAL",
				"error":     "ERROR",
				"err":       "ERROR",
				"warn":      "WARNING",
				"warning":   "WARNING",
				"notice":    "NOTICE",
				"info":      "INFO",
				"debug":     "DEBUG",
			},
			MapValuesExclusive: true,
		},
		InstrumentationSourceLabel: instrumentationSourceValue(p.Type()),
	}

	// Generate the httpRequest structure.
	for _, field := range []string{
		"serverIp",
		"remoteIp",
		"requestMethod",
		"status",
		"responseSize",
	} {
		fields[fmt.Sprintf("httpRequest.%s", field)] = &confgenerator.ModifyField{
			MoveFrom: fmt.Sprintf("jsonPayload.http_request_%s", field),
		}
	}

	// Log levels documented: https://docs.couchdb.org/en/stable/config/logging.html#log/level
	c = append(c,
		confgenerator.LoggingProcessorModifyFields{
			Fields: fields,
		}.Components(ctx, tag, uid)...,
	)
	return c
}

type LoggingReceiverCouchdb struct {
	LoggingProcessorCouchdb `yaml:",inline"`
	ReceiverMixin           confgenerator.LoggingReceiverFilesMixin `yaml:",inline" validate:"structonly"`
}

func (r LoggingReceiverCouchdb) Components(ctx context.Context, tag string) []fluentbit.Component {
	if len(r.ReceiverMixin.IncludePaths) == 0 {
		r.ReceiverMixin.IncludePaths = []string{
			// Default log file
			"/var/log/couchdb/couchdb.log",
		}
	}
	r.ReceiverMixin.MultilineRules = []confgenerator.MultilineRule{
		{
			StateName: "start_state",
			NextState: "cont",
			Regex:     `^\[\w+\]`,
		},
		{
			StateName: "cont",
			NextState: "cont",
			Regex:     `^(?!\[\w+\])`,
		},
	}

	c := r.ReceiverMixin.Components(ctx, tag)
	c = append(c, r.LoggingProcessorCouchdb.Components(ctx, tag, "couchdb")...)
	return c
}

func init() {
	confgenerator.LoggingProcessorTypes.RegisterType(func() confgenerator.LoggingProcessor { return &LoggingProcessorCouchdb{} })
	confgenerator.LoggingReceiverTypes.RegisterType(func() confgenerator.LoggingReceiver { return &LoggingReceiverCouchdb{} })
}
