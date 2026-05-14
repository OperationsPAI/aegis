package model

import (
	"time"

	"aegis/platform/consts"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
)

// FaultInjectionSchedule model
type FaultInjection struct {
	ID                int                   `gorm:"primaryKey;autoIncrement"`                                              // Unique identifier
	Name              string                `gorm:"size:128;not null"`                                                     // Schedule name, add unique index
	Source            consts.DatapackSource `gorm:"size:32;not null;default:'injection'"`                                  // Data source: injection or manual
	FaultType         chaos.ChaosType       `gorm:"not null;index:idx_fault_type_state"`                                   // Fault type, add composite index
	Category          chaos.SystemType      `gorm:"not null"`                                                              // System category
	Description       string                `gorm:"type:text"`                                                             // Description
	DisplayConfig     *string               `gorm:"type:longtext"`                                                         // User-facing display configuration
	EngineConfig      string                `gorm:"type:longtext;not null"`                                                // System-facing runtime configuration
	Groundtruths      []Groundtruth         `gorm:"type:json;serializer:json"`                                             // Expected impact groundtruth (service, pod, container, metric, function, span)
	GroundtruthSource string                `gorm:"size:32;not null;default:'auto'" json:"groundtruth_source"`             // Ground truth source: auto, manual, imported
	PreDuration       int                   `gorm:"not null"`                                                              // Normal data duration
	StartTime         *time.Time            `gorm:"check:start_time IS NULL OR end_time IS NULL OR start_time < end_time"` // Expected fault start time, nullable with validation
	EndTime           *time.Time            // Expected fault end time, nullable
	BenchmarkID       *int                  `gorm:"index:idx_fault_bench_ped"` // Associated benchmark ID, nullable for manual uploads
	PedestalID        *int                  `gorm:"index:idx_fault_bench_ped"` // Associated pedestal ID, nullable for manual uploads
	TaskID            *string               `gorm:"index;size:64"`             // Associated task ID, add composite index

	State     consts.DatapackState `gorm:"not null;default:0;index:idx_fault_type_state"` // Datapack state
	Status    consts.StatusType    `gorm:"not null;default:1;index"`                      // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time            `gorm:"autoCreateTime"`                                // Creation time, add time index
	UpdatedAt time.Time            `gorm:"autoUpdateTime"`                                // Update time (removed index - rarely queried)

	// Foreign key association with cascade
	Benchmark *ContainerVersion `gorm:"foreignKey:BenchmarkID;constraint:OnDelete:SET NULL"`
	Pedestal  *ContainerVersion `gorm:"foreignKey:PedestalID;constraint:OnDelete:SET NULL"`
	Task      *Task             `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE"`

	// Many-to-many relationship with labels
	Labels []Label `gorm:"many2many:fault_injection_labels"`
}

type Execution struct {
	ID                 int     `gorm:"primaryKey;autoIncrement"`              // Unique identifier
	Duration           float64 `gorm:"not null;default:0"`                    // Execution duration
	TaskID             *string `gorm:"index;size:64"`                         // Associated task ID, add composite index
	AlgorithmVersionID int     `gorm:"not null;index:idx_exec_algo_datapack"` // Algorithm ID, add composite index
	DatapackID         int     `gorm:"not null;index:idx_exec_algo_datapack"` // Datapack identifier, add composite index
	DatasetVersionID   *int    // Dataset identifier (optional, for dataset-based executions)

	State     consts.ExecutionState `gorm:"not null;default:0;index"` // Execution state
	Status    consts.StatusType     `gorm:"not null;default:1;index"` // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time             `gorm:"autoCreateTime"`           // CreatedAt automatically set to current time
	UpdatedAt time.Time             `gorm:"autoUpdateTime"`           // UpdatedAt automatically updates time

	// Foreign key association with cascade
	Task             *Task             `gorm:"foreignKey:TaskID;constraint:OnDelete:CASCADE"`
	AlgorithmVersion *ContainerVersion `gorm:"foreignKey:AlgorithmVersionID;constraint:OnDelete:RESTRICT"`
	Datapack         *FaultInjection   `gorm:"foreignKey:DatapackID;constraint:OnDelete:RESTRICT"`
	DatasetVersion   *DatasetVersion   `gorm:"foreignKey:DatasetVersionID;constraint:OnDelete:SET NULL"`

	DetectorResults    []DetectorResult    `gorm:"foreignKey:ExecutionID"`
	GranularityResults []GranularityResult `gorm:"foreignKey:ExecutionID"`

	// Many-to-many relationship with labels
	Labels []Label `gorm:"many2many:execution_injection_labels"`
}

type DetectorResult struct {
	ID                  int      `gorm:"primaryKey"`        // Unique identifier
	SpanName            string   `gorm:"type:varchar(255)"` // SpanName database field type
	Issues              string   `gorm:"type:text"`         // Issues field type is text
	AbnormalAvgDuration *float64 `gorm:"type:float"`        // Average duration during abnormal period
	NormalAvgDuration   *float64 `gorm:"type:float"`        // Average duration during normal period
	AbnormalSuccRate    *float64 `gorm:"type:float"`        // Success rate during abnormal period
	NormalSuccRate      *float64 `gorm:"type:float"`        // Success rate during normal period
	AbnormalP90         *float64 `gorm:"type:float"`        // P90 during abnormal period
	NormalP90           *float64 `gorm:"type:float"`        // P90 during normal period
	AbnormalP95         *float64 `gorm:"type:float"`        // P95 during abnormal period
	NormalP95           *float64 `gorm:"type:float"`        // P95 during normal period
	AbnormalP99         *float64 `gorm:"type:float"`        // P99 during abnormal period
	NormalP99           *float64 `gorm:"type:float"`        // P99 during normal period
	ExecutionID         int      // Associated Execution ID

	// Foreign key association
	Execution *Execution `gorm:"foreignKey:ExecutionID;constraint:OnDelete:CASCADE"`
}

type GranularityResult struct {
	ID          int     `gorm:"primaryKey;autoIncrement"`  // Unique identifier
	Level       string  `gorm:"not null;type:varchar(50)"` // Granularity type (e.g., "service", "pod", "span", "metric")
	Result      string  // Localization result, comma-separated
	Rank        int     // Ranking, representing top1, top2, etc.
	Confidence  float64 // Confidence level (optional)
	ExecutionID int     `gorm:"index"` // Associated Execution ID

	// Foreign key association
	Execution *Execution `gorm:"foreignKey:ExecutionID;constraint:OnDelete:CASCADE"`
}

// FaultInjectionLabel Many-to-many relationship table between FaultInjection and Label
type FaultInjectionLabel struct {
	FaultInjectionID int `gorm:"primaryKey"` // Fault injection ID
	LabelID          int `gorm:"primaryKey"` // Label ID

	// Foreign key association
	FaultInjection *FaultInjection `gorm:"foreignKey:FaultInjectionID;constraint:OnDelete:CASCADE"`
	Label          *Label          `gorm:"foreignKey:LabelID"`
}

// ExecutionInjectionLabel Many-to-many relationship table between Execution and Label
type ExecutionInjectionLabel struct {
	ExecutionID int       `gorm:"primaryKey"`     // Execution ID
	LabelID     int       `gorm:"primaryKey"`     // Label ID
	CreatedAt   time.Time `gorm:"autoCreateTime"` // Creation time

	// Foreign key association
	Execution *Execution `gorm:"foreignKey:ExecutionID;constraint:OnDelete:CASCADE"`
	Label     *Label     `gorm:"foreignKey:LabelID"`
}
