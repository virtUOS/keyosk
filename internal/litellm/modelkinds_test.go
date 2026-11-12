package litellm

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/virtuos/keyosk/internal/keyprovider"
)

// modelKinds asks a gateway that answers /model/info with body.
func modelKinds(t *testing.T, body string) map[string]string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, "mk").ModelKinds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The dashboard shows an example request per model, and an embedding model, a
// reranker and an image generator each take a different endpoint and body
// than a chat model. The gateway reports which is which in model_info.mode,
// and a model that reads images in supports_vision, so the portal must read
// them rather than assume every model is a plain chat model.
func TestModelKindsReadFromGateway(t *testing.T) {
	got := modelKinds(t, `{"data":[
		{"model_name":"chat-a","model_info":{"mode":"chat"}},
		{"model_name":"bge-m3","model_info":{"mode":"embedding"}},
		{"model_name":"test/bge-m3","model_info":{"mode":"embedding"}},
		{"model_name":"BAAI/bge-reranker-v2-m3","model_info":{"mode":"rerank"}},
		{"model_name":"gpt-image-1","model_info":{"mode":"image_generation"}},
		{"model_name":"lightonai/LightOnOCR-3-4B","model_info":{"mode":"chat","supports_vision":true}},
		{"model_name":"no-mode-vision","model_info":{"supports_vision":true}},
		{"model_name":"text-only","model_info":{"mode":"chat","supports_vision":false}}
	]}`)
	want := map[string]string{
		"bge-m3":                    keyprovider.KindEmbedding,
		"test/bge-m3":               keyprovider.KindEmbedding,
		"BAAI/bge-reranker-v2-m3":   keyprovider.KindRerank,
		"gpt-image-1":               keyprovider.KindImage,
		"lightonai/LightOnOCR-3-4B": keyprovider.KindVision,
		"no-mode-vision":            keyprovider.KindVision,
	}
	if !maps.Equal(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

// The mode says what endpoint a model takes, and an embedding model, a
// reranker or an image generator that also reads images still takes its own:
// the chat example with a page would fail against it.
func TestModelKindsModeWinsOverVision(t *testing.T) {
	got := modelKinds(t, `{"data":[
		{"model_name":"e","model_info":{"mode":"embedding","supports_vision":true}},
		{"model_name":"r","model_info":{"mode":"rerank","supports_vision":true}},
		{"model_name":"i","model_info":{"mode":"image_generation","supports_vision":true}}
	]}`)
	want := map[string]string{
		"e": keyprovider.KindEmbedding,
		"r": keyprovider.KindRerank,
		"i": keyprovider.KindImage,
	}
	if !maps.Equal(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}

// A model the gateway does not classify, or classifies in a way the portal
// has no example for, must get the chat example: several models report a
// null mode, and a chat example is the safe default — it is what nearly every
// model here is.
func TestModelKindsTreatUnknownModeAsChat(t *testing.T) {
	got := modelKinds(t, `{"data":[
		{"model_name":"no-mode"},
		{"model_name":"null-mode","model_info":{"mode":null,"supports_vision":null}},
		{"model_name":"unknown-mode","model_info":{"mode":"audio_speech"}},
		{"model_name":"","model_info":{"mode":"embedding"}}
	]}`)
	if len(got) != 0 {
		t.Errorf("got %v, want every model treated as chat", got)
	}
}

// A model served by several deployments is listed once for each. One that
// carries no kind must not undo the kind another reports.
func TestModelKindsKeepAKindAcrossDeployments(t *testing.T) {
	got := modelKinds(t, `{"data":[
		{"model_name":"bge-m3","model_info":{"mode":"embedding"}},
		{"model_name":"bge-m3","model_info":{}}
	]}`)
	if got["bge-m3"] != keyprovider.KindEmbedding {
		t.Errorf("kinds = %v, want bge-m3 still an embedding model", got)
	}
}
