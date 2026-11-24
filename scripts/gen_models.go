// Command gen_models regenerates data/models.json from the compact base-model
// definitions below. It uses the same scoring code as the CLI, so catalog scores
// can never drift from the implementation.
//
//	go run ./scripts/gen_models.go
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"llmfit/internal/catalog"
	"llmfit/internal/scoring"
)

type baseModel struct {
	Slug       string
	Family     string
	Display    string
	License    string
	OllamaBase string
	HFRepo     string
	Params     float64
	Active     float64
	Layers     int
	KVHeads    int
	HeadDim    int
	MaxContext int
	Tasks      []string
	Year       int
	Quants     []string
}

func q4() []string   { return []string{"Q4_K_M", "Q5_K_M"} }
func q48() []string  { return []string{"Q4_K_M", "Q8_0"} }
func q458() []string { return []string{"Q4_K_M", "Q5_K_M", "Q8_0"} }

func baseModels() []baseModel {
	return []baseModel{
		{"llama-3.2-1b-instruct", "Llama 3.2", "Llama 3.2 1B Instruct", "Llama 3.2 Community", "llama3.2:1b-instruct", "bartowski/Llama-3.2-1B-Instruct-GGUF", 1.24, 1.24, 16, 8, 64, 131072, []string{"chat", "general"}, 2024, []string{"Q4_K_M", "Q8_0", "FP16"}},
		{"llama-3.2-3b-instruct", "Llama 3.2", "Llama 3.2 3B Instruct", "Llama 3.2 Community", "llama3.2:3b-instruct", "bartowski/Llama-3.2-3B-Instruct-GGUF", 3.21, 3.21, 28, 8, 128, 131072, []string{"chat", "general"}, 2024, q458()},
		{"llama-3.1-8b-instruct", "Llama 3.1", "Llama 3.1 8B Instruct", "Llama 3.1 Community", "llama3.1:8b-instruct", "bartowski/Meta-Llama-3.1-8B-Instruct-GGUF", 8.03, 8.03, 32, 8, 128, 131072, []string{"chat", "general"}, 2024, q458()},
		{"llama-3.1-70b-instruct", "Llama 3.1", "Llama 3.1 70B Instruct", "Llama 3.1 Community", "llama3.1:70b-instruct", "bartowski/Meta-Llama-3.1-70B-Instruct-GGUF", 70.6, 70.6, 80, 8, 128, 131072, []string{"chat", "general"}, 2024, q4()},
		{"llama-3.3-70b-instruct", "Llama 3.3", "Llama 3.3 70B Instruct", "Llama 3.3 Community", "llama3.3:70b-instruct", "bartowski/Llama-3.3-70B-Instruct-GGUF", 70.6, 70.6, 80, 8, 128, 131072, []string{"chat", "general", "reasoning"}, 2024, q4()},
		{"llama-4-scout-17b-16e", "Llama 4", "Llama 4 Scout 17Bx16E", "Llama 4 Community", "llama4:scout", "bartowski/Llama-4-Scout-17B-16E-Instruct-GGUF", 109, 17, 48, 8, 128, 10485760, []string{"chat", "general"}, 2025, []string{"Q4_K_M"}},

		{"qwen2.5-0.5b-instruct", "Qwen2.5", "Qwen2.5 0.5B Instruct", "Apache-2.0", "qwen2.5:0.5b-instruct", "bartowski/Qwen2.5-0.5B-Instruct-GGUF", 0.49, 0.49, 24, 2, 64, 32768, []string{"chat", "general"}, 2024, []string{"Q4_K_M", "Q8_0", "FP16"}},
		{"qwen2.5-1.5b-instruct", "Qwen2.5", "Qwen2.5 1.5B Instruct", "Apache-2.0", "qwen2.5:1.5b-instruct", "bartowski/Qwen2.5-1.5B-Instruct-GGUF", 1.54, 1.54, 28, 2, 128, 32768, []string{"chat", "general"}, 2024, []string{"Q4_K_M", "Q8_0", "FP16"}},
		{"qwen2.5-3b-instruct", "Qwen2.5", "Qwen2.5 3B Instruct", "Qwen License", "qwen2.5:3b-instruct", "bartowski/Qwen2.5-3B-Instruct-GGUF", 3.09, 3.09, 36, 2, 128, 32768, []string{"chat", "general"}, 2024, q458()},
		{"qwen2.5-7b-instruct", "Qwen2.5", "Qwen2.5 7B Instruct", "Apache-2.0", "qwen2.5:7b-instruct", "bartowski/Qwen2.5-7B-Instruct-GGUF", 7.62, 7.62, 28, 4, 128, 32768, []string{"chat", "general"}, 2024, q458()},
		{"qwen2.5-14b-instruct", "Qwen2.5", "Qwen2.5 14B Instruct", "Apache-2.0", "qwen2.5:14b-instruct", "bartowski/Qwen2.5-14B-Instruct-GGUF", 14.7, 14.7, 48, 8, 128, 32768, []string{"chat", "general"}, 2024, q4()},
		{"qwen2.5-32b-instruct", "Qwen2.5", "Qwen2.5 32B Instruct", "Apache-2.0", "qwen2.5:32b-instruct", "bartowski/Qwen2.5-32B-Instruct-GGUF", 32.5, 32.5, 64, 8, 128, 32768, []string{"chat", "general", "reasoning"}, 2024, q4()},
		{"qwen2.5-72b-instruct", "Qwen2.5", "Qwen2.5 72B Instruct", "Qwen License", "qwen2.5:72b-instruct", "bartowski/Qwen2.5-72B-Instruct-GGUF", 72.7, 72.7, 80, 8, 128, 32768, []string{"chat", "general"}, 2024, []string{"Q4_K_M"}},

		{"qwen2.5-coder-1.5b-instruct", "Qwen2.5-Coder", "Qwen2.5 Coder 1.5B Instruct", "Apache-2.0", "qwen2.5-coder:1.5b-instruct", "bartowski/Qwen2.5-Coder-1.5B-Instruct-GGUF", 1.54, 1.54, 28, 2, 128, 32768, []string{"coding", "chat"}, 2024, q48()},
		{"qwen2.5-coder-7b-instruct", "Qwen2.5-Coder", "Qwen2.5 Coder 7B Instruct", "Apache-2.0", "qwen2.5-coder:7b-instruct", "bartowski/Qwen2.5-Coder-7B-Instruct-GGUF", 7.62, 7.62, 28, 4, 128, 32768, []string{"coding", "chat"}, 2024, q4()},
		{"qwen2.5-coder-14b-instruct", "Qwen2.5-Coder", "Qwen2.5 Coder 14B Instruct", "Apache-2.0", "qwen2.5-coder:14b-instruct", "bartowski/Qwen2.5-Coder-14B-Instruct-GGUF", 14.7, 14.7, 48, 8, 128, 32768, []string{"coding", "chat"}, 2024, q4()},
		{"qwen2.5-coder-32b-instruct", "Qwen2.5-Coder", "Qwen2.5 Coder 32B Instruct", "Apache-2.0", "qwen2.5-coder:32b-instruct", "bartowski/Qwen2.5-Coder-32B-Instruct-GGUF", 32.5, 32.5, 64, 8, 128, 32768, []string{"coding", "chat"}, 2024, []string{"Q4_K_M"}},

		{"qwen3-0.6b", "Qwen3", "Qwen3 0.6B", "Apache-2.0", "qwen3:0.6b", "bartowski/Qwen_Qwen3-0.6B-GGUF", 0.6, 0.6, 28, 8, 128, 32768, []string{"chat", "general", "reasoning"}, 2025, q48()},
		{"qwen3-1.7b", "Qwen3", "Qwen3 1.7B", "Apache-2.0", "qwen3:1.7b", "bartowski/Qwen_Qwen3-1.7B-GGUF", 1.7, 1.7, 28, 8, 128, 32768, []string{"chat", "general", "reasoning"}, 2025, q48()},
		{"qwen3-4b", "Qwen3", "Qwen3 4B", "Apache-2.0", "qwen3:4b", "bartowski/Qwen_Qwen3-4B-GGUF", 4.0, 4.0, 36, 8, 128, 131072, []string{"chat", "general", "reasoning"}, 2025, q4()},
		{"qwen3-8b", "Qwen3", "Qwen3 8B", "Apache-2.0", "qwen3:8b", "bartowski/Qwen_Qwen3-8B-GGUF", 8.2, 8.2, 36, 8, 128, 131072, []string{"chat", "general", "reasoning"}, 2025, q458()},
		{"qwen3-14b", "Qwen3", "Qwen3 14B", "Apache-2.0", "qwen3:14b", "bartowski/Qwen_Qwen3-14B-GGUF", 14.8, 14.8, 40, 8, 128, 131072, []string{"chat", "general", "reasoning"}, 2025, []string{"Q4_K_M"}},
		{"qwen3-32b", "Qwen3", "Qwen3 32B", "Apache-2.0", "qwen3:32b", "bartowski/Qwen_Qwen3-32B-GGUF", 32.8, 32.8, 64, 8, 128, 131072, []string{"chat", "general", "reasoning"}, 2025, []string{"Q4_K_M"}},
		{"qwen3-30b-a3b", "Qwen3", "Qwen3 30B A3B", "Apache-2.0", "qwen3:30b-a3b", "bartowski/Qwen_Qwen3-30B-A3B-GGUF", 30.5, 3.3, 48, 4, 128, 131072, []string{"chat", "general", "reasoning"}, 2025, []string{"Q4_K_M"}},
		{"qwen3-coder-30b-a3b", "Qwen3-Coder", "Qwen3 Coder 30B A3B", "Apache-2.0", "qwen3-coder:30b-a3b", "bartowski/Qwen_Qwen3-Coder-30B-A3B-Instruct-GGUF", 30.5, 3.3, 48, 4, 128, 131072, []string{"coding", "chat"}, 2025, []string{"Q4_K_M"}},

		{"gemma-3-1b-it", "Gemma 3", "Gemma 3 1B IT", "Gemma Terms of Use", "gemma3:1b-it", "bartowski/google_gemma-3-1b-it-GGUF", 1.0, 1.0, 26, 4, 256, 32768, []string{"chat", "general"}, 2025, []string{"Q4_K_M", "Q8_0", "FP16"}},
		{"gemma-3-4b-it", "Gemma 3", "Gemma 3 4B IT", "Gemma Terms of Use", "gemma3:4b-it", "bartowski/google_gemma-3-4b-it-GGUF", 4.3, 4.3, 34, 4, 256, 131072, []string{"chat", "general"}, 2025, q4()},
		{"gemma-3-12b-it", "Gemma 3", "Gemma 3 12B IT", "Gemma Terms of Use", "gemma3:12b-it", "bartowski/google_gemma-3-12b-it-GGUF", 12.2, 12.2, 48, 8, 256, 131072, []string{"chat", "general"}, 2025, q4()},
		{"gemma-3-27b-it", "Gemma 3", "Gemma 3 27B IT", "Gemma Terms of Use", "gemma3:27b-it", "bartowski/google_gemma-3-27b-it-GGUF", 27.4, 27.4, 62, 8, 256, 131072, []string{"chat", "general"}, 2025, []string{"Q4_K_M"}},
		{"gemma-2-9b-it", "Gemma 2", "Gemma 2 9B IT", "Gemma Terms of Use", "gemma2:9b-instruct", "bartowski/gemma-2-9b-it-GGUF", 9.24, 9.24, 42, 8, 256, 8192, []string{"chat", "general"}, 2024, q4()},

		{"mistral-7b-instruct-v0.3", "Mistral", "Mistral 7B Instruct v0.3", "Apache-2.0", "mistral:7b-instruct", "bartowski/Mistral-7B-Instruct-v0.3-GGUF", 7.25, 7.25, 32, 8, 128, 32768, []string{"chat", "general"}, 2024, q458()},
		{"mistral-small-3.1-24b-instruct", "Mistral Small", "Mistral Small 3.1 24B Instruct", "Apache-2.0", "mistral-small3.1:24b-instruct-2503", "bartowski/Mistral-Small-3.1-24B-Instruct-2503-GGUF", 23.6, 23.6, 40, 8, 128, 131072, []string{"chat", "general"}, 2025, q4()},
		{"mixtral-8x7b-instruct", "Mixtral", "Mixtral 8x7B Instruct", "Apache-2.0", "mixtral:8x7b-instruct-v0.1", "TheBloke/Mixtral-8x7B-Instruct-v0.1-GGUF", 46.7, 12.9, 32, 8, 128, 32768, []string{"chat", "general"}, 2023, q4()},
		{"mistral-nemo-12b-instruct", "Mistral Nemo", "Mistral Nemo 12B Instruct", "Apache-2.0", "mistral-nemo:12b-instruct-2407", "bartowski/Mistral-Nemo-Instruct-2407-GGUF", 12.2, 12.2, 40, 8, 128, 131072, []string{"chat", "general"}, 2024, q4()},

		{"phi-4-14b", "Phi", "Phi-4 14B", "MIT", "phi4:14b", "bartowski/phi-4-GGUF", 14.7, 14.7, 40, 10, 128, 16384, []string{"chat", "reasoning", "general"}, 2024, q4()},
		{"phi-3.5-mini-instruct", "Phi", "Phi-3.5 Mini Instruct", "MIT", "phi3.5:3.8b-mini-instruct", "bartowski/Phi-3.5-mini-instruct-GGUF", 3.82, 3.82, 32, 32, 96, 131072, []string{"chat", "general"}, 2024, q48()},

		{"deepseek-r1-distill-qwen-1.5b", "DeepSeek R1 Distill", "DeepSeek R1 Distill Qwen 1.5B", "MIT", "deepseek-r1:1.5b", "bartowski/DeepSeek-R1-Distill-Qwen-1.5B-GGUF", 1.78, 1.78, 28, 2, 128, 131072, []string{"reasoning", "chat"}, 2025, q48()},
		{"deepseek-r1-distill-qwen-7b", "DeepSeek R1 Distill", "DeepSeek R1 Distill Qwen 7B", "MIT", "deepseek-r1:7b", "bartowski/DeepSeek-R1-Distill-Qwen-7B-GGUF", 7.62, 7.62, 28, 4, 128, 131072, []string{"reasoning", "chat"}, 2025, q4()},
		{"deepseek-r1-distill-qwen-14b", "DeepSeek R1 Distill", "DeepSeek R1 Distill Qwen 14B", "MIT", "deepseek-r1:14b", "bartowski/DeepSeek-R1-Distill-Qwen-14B-GGUF", 14.7, 14.7, 48, 8, 128, 131072, []string{"reasoning", "chat"}, 2025, []string{"Q4_K_M"}},
		{"deepseek-r1-distill-llama-32b", "DeepSeek R1 Distill", "DeepSeek R1 Distill Llama 32B", "MIT", "deepseek-r1:32b", "bartowski/DeepSeek-R1-Distill-Llama-32B-GGUF", 32.8, 32.8, 64, 8, 128, 131072, []string{"reasoning", "chat"}, 2025, []string{"Q4_K_M"}},
		{"deepseek-r1-distill-llama-70b", "DeepSeek R1 Distill", "DeepSeek R1 Distill Llama 70B", "MIT", "deepseek-r1:70b", "bartowski/DeepSeek-R1-Distill-Llama-70B-GGUF", 70.6, 70.6, 80, 8, 128, 131072, []string{"reasoning", "chat"}, 2025, []string{"Q4_K_M"}},

		{"codestral-22b", "Codestral", "Codestral 22B", "Mistral AI Non-Production", "codestral:22b-v0.1", "bartowski/Codestral-22B-v0.1-GGUF", 22.2, 22.2, 56, 8, 128, 32768, []string{"coding", "chat"}, 2024, q4()},
		{"codegemma-7b-instruct", "CodeGemma", "CodeGemma 7B Instruct", "Gemma Terms of Use", "codegemma:7b-instruct", "bartowski/codegemma-7b-it-GGUF", 8.5, 8.5, 28, 8, 128, 8192, []string{"coding", "chat"}, 2024, q48()},

		{"granite-3.1-8b-instruct", "Granite 3.1", "Granite 3.1 8B Instruct", "Apache-2.0", "granite3.1-dense:8b-instruct", "bartowski/granite-3.1-8b-instruct-GGUF", 8.2, 8.2, 40, 8, 128, 131072, []string{"chat", "general"}, 2024, q4()},
		{"granite-3.1-2b-instruct", "Granite 3.1", "Granite 3.1 2B Instruct", "Apache-2.0", "granite3.1-dense:2b-instruct", "bartowski/granite-3.1-2b-instruct-GGUF", 2.0, 2.0, 40, 8, 128, 131072, []string{"chat", "general"}, 2024, q48()},
		{"olmo-2-7b-instruct", "OLMo 2", "OLMo 2 7B Instruct", "Apache-2.0", "olmo2:7b-instruct", "bartowski/OLMo-2-1124-7B-Instruct-GGUF", 7.3, 7.3, 32, 32, 128, 4096, []string{"chat", "general"}, 2024, q48()},
		{"smollm2-1.7b-instruct", "SmolLM2", "SmolLM2 1.7B Instruct", "Apache-2.0", "smollm2:1.7b-instruct", "bartowski/SmolLM2-1.7B-Instruct-GGUF", 1.7, 1.7, 24, 8, 64, 8192, []string{"chat", "general"}, 2024, q48()},
		{"hermes-3-llama-3.1-8b", "Hermes 3", "Hermes 3 Llama 3.1 8B", "Llama 3.1 Community", "hermes3:8b-llama3.1", "bartowski/Hermes-3-Llama-3.1-8B-GGUF", 8.03, 8.03, 32, 8, 128, 131072, []string{"chat", "general"}, 2024, []string{"Q4_K_M"}},
		{"yi-1.5-9b-chat", "Yi 1.5", "Yi 1.5 9B Chat", "Apache-2.0", "yi:9b-chat-v1.5", "bartowski/Yi-1.5-9B-Chat-GGUF", 8.8, 8.8, 48, 8, 128, 16384, []string{"chat", "general"}, 2024, q4()},
		{"nemotron-mini-4b-instruct", "Nemotron", "Nemotron Mini 4B Instruct", "NVIDIA Open Model License", "nemotron-mini:4b-instruct", "bartowski/nemotron-mini-4b-instruct-GGUF", 4.2, 4.2, 32, 8, 128, 131072, []string{"chat", "general"}, 2024, []string{"Q4_K_M"}},
	}
}

func quantTag(q string) string {
	switch q {
	case "Q4_K_M":
		return "q4_K_M"
	case "Q5_K_M":
		return "q5_K_M"
	case "Q6_K":
		return "q6_K"
	case "Q8_0":
		return "q8_0"
	case "Q3_K_M":
		return "q3_K_M"
	case "FP16":
		return "fp16"
	}
	return strings.ToLower(q)
}

func fileSizeGB(params float64, quant string) float64 {
	bpw := scoring.BitsPerWeight[quant]
	size := params * bpw / 8
	if params < 2 {
		size += 0.12 // tokenizer/embedding overhead matters more for tiny models
	}
	return math.Round(size*100) / 100
}

func main() {
	c := &catalog.Catalog{Version: 1, ScoreContext: 8192}
	for _, b := range baseModels() {
		active := b.Active
		if active == 0 {
			active = b.Params
		}
		for _, q := range b.Quants {
			tag := b.OllamaBase + "-" + quantTag(q)
			if strings.HasPrefix(b.Slug, "llama-4") {
				tag = b.OllamaBase
			}
			repoBase := b.HFRepo
			if idx := strings.LastIndex(repoBase, "/"); idx >= 0 {
				repoBase = repoBase[idx+1:]
			}
			c.Models = append(c.Models, catalog.Model{
				ID:            b.Slug + "-" + strings.ToLower(q),
				Family:        b.Family,
				DisplayName:   b.Display,
				ParamsB:       b.Params,
				ActiveParamsB: active,
				Quant:         q,
				FileSizeGB:    fileSizeGB(b.Params, q),
				MaxContext:    b.MaxContext,
				Tasks:         b.Tasks,
				License:       b.License,
				ReleaseYear:   b.Year,
				Arch:          catalog.Arch{Layers: b.Layers, KVHeads: b.KVHeads, HeadDim: b.HeadDim},
				Runtimes: catalog.Runtimes{
					Ollama:   tag,
					LlamaCpp: b.HFRepo,
					LMStudio: "lmstudio-community/" + repoBase,
				},
			})
		}
	}
	c.RecomputeScores()
	if errs := c.Validate(); len(errs) > 0 {
		for _, err := range errs {
			fmt.Fprintln(os.Stderr, "catalog error:", err)
		}
		os.Exit(1)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b = append(b, '\n')
	out := filepath.Join("data", "models.json")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s: %d models, %d families\n", out, len(c.Models), countFamilies(c))
}

func countFamilies(c *catalog.Catalog) int {
	seen := map[string]bool{}
	for _, m := range c.Models {
		seen[m.Family] = true
	}
	return len(seen)
}
