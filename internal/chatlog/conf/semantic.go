package conf

import "strings"

const (
	DefaultGLMBaseURL        = "https://open.bigmodel.cn/api/paas/v4"
	DefaultGLMEmbedding      = "embedding-3"
	DefaultGLMRerank         = "rerank"
	DefaultGLMEmbeddingDim   = 512
	DefaultSemanticRecallK   = 80
	DefaultSemanticTopN      = 20
	DefaultSemanticThreshold = 0.55
	DefaultSemanticWorkers   = 4
)

type SemanticConfig struct {
	Enabled             bool    `mapstructure:"enabled" json:"enabled"`
	APIKey              string  `mapstructure:"api_key" json:"api_key"`
	BaseURL             string  `mapstructure:"base_url" json:"base_url"`
	EmbeddingModel      string  `mapstructure:"embedding_model" json:"embedding_model"`
	RerankModel         string  `mapstructure:"rerank_model" json:"rerank_model"`
	EmbeddingDimension  int     `mapstructure:"embedding_dimension" json:"embedding_dimension"`
	EnableRerank        bool    `mapstructure:"enable_rerank" json:"enable_rerank"`
	EnableSemanticPush  bool    `mapstructure:"enable_semantic_push" json:"enable_semantic_push"`
	EnableQA            bool    `mapstructure:"enable_qa" json:"enable_qa"`
	EnableTopics        bool    `mapstructure:"enable_topics" json:"enable_topics"`
	EnableProfiles      bool    `mapstructure:"enable_profiles" json:"enable_profiles"`
	RealtimeIndex       bool    `mapstructure:"realtime_index" json:"realtime_index"`
	IndexWorkers        int     `mapstructure:"index_workers" json:"index_workers"`
	RecallK             int     `mapstructure:"recall_k" json:"recall_k"`
	TopN                int     `mapstructure:"top_n" json:"top_n"`
	SimilarityThreshold float64 `mapstructure:"similarity_threshold" json:"similarity_threshold"`
}

func NormalizeSemanticConfig(in SemanticConfig) SemanticConfig {
	out := in
	out.BaseURL = strings.TrimSpace(out.BaseURL)
	if out.BaseURL == "" {
		out.BaseURL = DefaultGLMBaseURL
	}
	out.EmbeddingModel = strings.TrimSpace(out.EmbeddingModel)
	if out.EmbeddingModel == "" {
		out.EmbeddingModel = DefaultGLMEmbedding
	}
	out.RerankModel = strings.TrimSpace(out.RerankModel)
	if out.RerankModel == "" {
		out.RerankModel = DefaultGLMRerank
	}
	if out.EmbeddingDimension < 256 || out.EmbeddingDimension > 2048 {
		out.EmbeddingDimension = DefaultGLMEmbeddingDim
	}
	if out.RecallK <= 0 {
		out.RecallK = DefaultSemanticRecallK
	}
	if out.IndexWorkers <= 0 {
		out.IndexWorkers = DefaultSemanticWorkers
	}
	if out.IndexWorkers > 32 {
		out.IndexWorkers = 32
	}
	if out.TopN <= 0 {
		out.TopN = DefaultSemanticTopN
	}
	if out.TopN > out.RecallK {
		out.TopN = out.RecallK
	}
	if out.SimilarityThreshold <= 0 || out.SimilarityThreshold > 1 {
		out.SimilarityThreshold = DefaultSemanticThreshold
	}
	return out
}
