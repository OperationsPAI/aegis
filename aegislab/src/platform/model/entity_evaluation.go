package model

import (
	"time"

	"aegis/platform/consts"

	"gorm.io/datatypes"
)

// Evaluation represents a persisted evaluation result
type Evaluation struct {
	ID               int               `gorm:"primaryKey;autoIncrement"`
	ProjectID        *int              `gorm:"index"`
	AlgorithmName    string            `gorm:"not null;size:128"`
	AlgorithmVersion string            `gorm:"not null;size:32"`
	DatapackName     string            `gorm:"size:128"`
	DatasetName      string            `gorm:"size:128"`
	DatasetVersion   string            `gorm:"size:32"`
	EvalType         string            `gorm:"not null;size:16"`
	Precision        float64           `gorm:"not null;default:0"`
	Recall           float64           `gorm:"not null;default:0"`
	F1Score          float64           `gorm:"not null;default:0"`
	Accuracy         float64           `gorm:"not null;default:0"`
	ResultJSON       string            `gorm:"type:text"`
	Status           consts.StatusType `gorm:"not null;default:1;index"`
	CreatedAt        time.Time         `gorm:"autoCreateTime"`
	UpdatedAt        time.Time         `gorm:"autoUpdateTime"`
}

// =====================================================================
// LLM evaluation (mirrored from rcabench-platform/llm_eval)
// =====================================================================

// EvaluationSample mirrors rcabench_platform.v3.sdk.llm_eval.db.eval_datapoint.EvaluationSample
// (table `evaluation_data`). Written by the Python LLM-eval pipeline; the Go
// side currently only owns the schema/migration.
type EvaluationSample struct {
	ID        int       `gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`

	// base info
	Dataset           string         `gorm:"column:dataset;not null;default:''"`
	DatasetIndex      *int           `gorm:"column:dataset_index"`
	Source            string         `gorm:"column:source;not null;default:''"`
	RawQuestion       string         `gorm:"column:raw_question;type:text"`
	Level             *int           `gorm:"column:level"`
	AugmentedQuestion string         `gorm:"column:augmented_question;type:text"`
	CorrectAnswer     string         `gorm:"column:correct_answer;type:text"`
	FileName          string         `gorm:"column:file_name"`
	Meta              datatypes.JSON `gorm:"column:meta"`

	// rollout
	TraceID      *string        `gorm:"column:trace_id"`
	TraceURL     *string        `gorm:"column:trace_url"`
	Response     string         `gorm:"column:response;type:text"`
	TimeCost     *float64       `gorm:"column:time_cost"`
	Trajectories datatypes.JSON `gorm:"column:trajectories"`

	// judgement
	ExtractedFinalAnswer string   `gorm:"column:extracted_final_answer;type:text"`
	JudgedResponse       string   `gorm:"column:judged_response;type:text"`
	Reasoning            string   `gorm:"column:reasoning;type:text"`
	Correct              *bool    `gorm:"column:correct"`
	Confidence           *float64 `gorm:"column:confidence"`

	// v2 metrics
	EvalMetrics datatypes.JSON `gorm:"column:eval_metrics"`

	// identifiers
	ExpID     string  `gorm:"column:exp_id;not null;default:'default';index"`
	AgentType *string `gorm:"column:agent_type;index"`
	ModelName *string `gorm:"column:model_name;index"`
	Stage     string  `gorm:"column:stage;not null;default:'init';index"`
}

func (EvaluationSample) TableName() string {
	return "evaluation_data"
}

// EvaluationRolloutStats mirrors rcabench_platform.v3.sdk.llm_eval.db.eval_datapoint.EvaluationRolloutStats
// (table `evaluation_rollout_stats`). 1:1 with EvaluationSample via shared PK.
type EvaluationRolloutStats struct {
	ID               int  `gorm:"column:id;primaryKey"`
	InputTokens      *int `gorm:"column:input_tokens"`
	OutputTokens     *int `gorm:"column:output_tokens"`
	CacheHitTokens   *int `gorm:"column:cache_hit_tokens"`
	CacheWriteTokens *int `gorm:"column:cache_write_tokens"`
	NLLMCalls        *int `gorm:"column:n_llm_calls"`

	Sample *EvaluationSample `gorm:"foreignKey:ID;references:ID"`
}

func (EvaluationRolloutStats) TableName() string {
	return "evaluation_rollout_stats"
}
