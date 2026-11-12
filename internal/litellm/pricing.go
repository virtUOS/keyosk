package litellm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

// Pricing is the one thing about model prices the portal still checks: which
// models are unpriced. A model with no price for anything it is billed by
// accrues no spend, so a budget over it never binds and the user is
// effectively unlimited on that model.
type Pricing struct {
	Unpriced []string
}

// pricedModel is the part of /model/info this needs: the prices a deployment
// can be given in its litellm_params, for each unit LiteLLM bills by.
type pricedModel struct {
	ModelName string `json:"model_name"`
	Params    struct {
		InputCost  float64 `json:"input_cost_per_token"`
		OutputCost float64 `json:"output_cost_per_token"`
		// An image model is billed per image or per pixel instead of per
		// token.
		InputCostPerImage  float64 `json:"input_cost_per_image"`
		OutputCostPerImage float64 `json:"output_cost_per_image"`
		InputCostPerPixel  float64 `json:"input_cost_per_pixel"`
		OutputCostPerPixel float64 `json:"output_cost_per_pixel"`
		// LiteLLM bills a rerank request at this price per search unit.
		InputCostPerQuery float64 `json:"input_cost_per_query"`
	} `json:"litellm_params"`
}

// priced reports whether the model has a price in any of those units.
func (m pricedModel) priced() bool {
	p := m.Params
	for _, cost := range []float64{
		p.InputCost, p.OutputCost,
		p.InputCostPerImage, p.OutputCostPerImage,
		p.InputCostPerPixel, p.OutputCostPerPixel,
		p.InputCostPerQuery,
	} {
		if cost > 0 {
			return true
		}
	}
	return false
}

// Pricing reads the gateway's model list and reports the unpriced ones.
func (c *Client) Pricing(ctx context.Context) (Pricing, error) {
	resp, err := c.do(ctx, http.MethodGet, "/model/info", nil)
	if err != nil {
		return Pricing{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return Pricing{}, fmt.Errorf("LiteLLM /model/info returned %d: %s", resp.StatusCode, b)
	}

	var body struct {
		Data []pricedModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Pricing{}, fmt.Errorf("decode model pricing: %w", err)
	}
	return summarisePricing(body.Data), nil
}

func summarisePricing(models []pricedModel) Pricing {
	var p Pricing
	for _, m := range models {
		if !m.priced() {
			p.Unpriced = append(p.Unpriced, m.ModelName)
		}
	}
	sort.Strings(p.Unpriced)
	return p
}
