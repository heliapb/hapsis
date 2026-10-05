package proxy

import (
	"bytes"
	"net/http"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	"github.com/grafana/tempo/v3/modules/frontend/combiner"
	"github.com/grafana/tempo/v3/pkg/api"
)

// pipelineResponse adapts a backend HTTP response to the interface Tempo's combiners consume.
// The combiner reads the body and headers itself.
// RequestData carries the backend index, which the search combiner uses as a shard id.
type pipelineResponse struct {
	resp *http.Response
	idx  int
}

var _ combiner.PipelineResponse = pipelineResponse{}

func (p pipelineResponse) HTTPResponse() *http.Response { return p.resp }
func (p pipelineResponse) RequestData() any             { return p.idx }
func (p pipelineResponse) IsMetadata() bool             { return false }

func writeMessage(w http.ResponseWriter, r *http.Request, msg proto.Message) {
	if api.MarshalingFormatFromAcceptHeader(r.Header) == api.MarshallingFormatProtobuf {
		b, err := proto.Marshal(msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set(api.HeaderContentType, api.HeaderAcceptProtobuf)
		_, _ = w.Write(b)
		return
	}
	var buf bytes.Buffer
	if err := new(jsonpb.Marshaler).Marshal(&buf, msg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set(api.HeaderContentType, api.HeaderAcceptJSON)
	_, _ = w.Write(buf.Bytes())
}
