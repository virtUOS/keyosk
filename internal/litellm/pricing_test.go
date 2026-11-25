package litellm

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// nearly compares spend figures that are summed rather than passed straight
// through, so they need not be bit-identical.
func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// A gateway that reports no models at all has nothing unpriced to warn about.
func TestPricingEmptyGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unpriced) != 0 {
		t.Errorf("Unpriced = %v, want none", p.Unpriced)
	}
}

// An image model is priced per image or per pixel, and a reranker per query,
// rather than per token. Such a model accrues spend all the same, so warning
// that a quota does not bind on it would be wrong.
func TestPricingCountsPricesOtherThanPerToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[
		  {"model_name":"free","litellm_params":{"input_cost_per_token":0,"output_cost_per_token":0}},
		  {"model_name":"per-image-out","litellm_params":{"output_cost_per_image":0.04}},
		  {"model_name":"per-image-in","litellm_params":{"input_cost_per_image":0.01}},
		  {"model_name":"per-pixel-out","litellm_params":{"output_cost_per_pixel":1e-8}},
		  {"model_name":"per-pixel-in","litellm_params":{"input_cost_per_pixel":1e-9}},
		  {"model_name":"per-query","litellm_params":{"input_cost_per_query":0.002}},
		  {"model_name":"null-image","litellm_params":{"output_cost_per_image":null,"input_cost_per_query":0}}
		]}`))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"free", "null-image"}; !slices.Equal(p.Unpriced, want) {
		t.Errorf("Unpriced = %v, want %v", p.Unpriced, want)
	}
}
