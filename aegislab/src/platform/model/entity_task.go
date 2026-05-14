package model

import (
	"time"

	"aegis/platform/consts"
)

// Trace model - Represents execution flow of related tasks
type Trace struct {
	ID        string           `gorm:"primaryKey;size:64"`                  // Trace ID (unique identifier for a workflow)
	Type      consts.TraceType `gorm:"not null;index:idx_trace_type_state"` // Trace type (datapack_build, algorithm_run, full_pipeline)
	LastEvent consts.EventType `gorm:"size:128"`                            // Last event type received (for quick status check)
	StartTime time.Time        `gorm:"not null"`                            // Trace start time
	EndTime   *time.Time       // Trace end time (null if not completed)
	GroupID   string           `gorm:"index;size:64"`                 // Group ID for batch operations
	ProjectID int              `gorm:"index:idx_trace_project_state"` // Associated project (optional)

	LeafNum int `gorm:"not null;default:1"` // Number of leaf nodes in the trace DAG

	State     consts.TraceState `gorm:"not null;default:0;index:idx_trace_type_state;index:idx_trace_project_state"` // Trace state (pending, running, completed, failed)
	Status    consts.StatusType `gorm:"not null;default:1;index"`                                                    // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`                                                              // Creation time
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`                                                              // Update time

	// Foreign key association
	Project *Project `gorm:"foreignKey:ProjectID"`

	// One-to-many relationship with tasks
	Tasks []Task `gorm:"foreignKey:TraceID;references:ID"`
}

// Task model
type Task struct {
	ID          string          `gorm:"primaryKey;size:64"`         // Task ID with size limit
	Type        consts.TaskType `gorm:"index:idx_task_type_status"` // Task type with size limit
	Immediate   bool            // Whether to execute immediately
	ExecuteTime int64           `gorm:"index"`         // Execution time timestamp
	CronExpr    string          `gorm:"size:128"`      // Cron expression with size limit
	Payload     string          `gorm:"type:text"`     // Task payload
	TraceID     string          `gorm:"index;size:64"` // Trace ID with size limit

	ParentTaskID *string `gorm:"index;size:64"`      // Parent task ID for sub-tasks
	Level        int     `gorm:"not null;default:0"` // Task level in the trace
	Sequence     int     `gorm:"not null;default:0"` // Task sequence in the trace

	State     consts.TaskState  `gorm:"not null;default:0;index:idx_task_type_state;index:idx_task_project_state"`   // Event type for the task Running
	Status    consts.StatusType `gorm:"not null;default:1;index:idx_task_type_status;index:idx_task_project_status"` // Status: -1:deleted 0:disabled 1:enabled
	CreatedAt time.Time         `gorm:"autoCreateTime"`                                                              // Creation time with index
	UpdatedAt time.Time         `gorm:"autoUpdateTime"`

	// Foreign key association
	Trace      *Trace `gorm:"foreignKey:TraceID;references:ID;constraint:OnDelete:CASCADE"`
	ParentTask *Task  `gorm:"foreignKey:ParentTaskID;references:ID;constraint:OnDelete:CASCADE"`

	// One-to-one back reference with cascade delete
	FaultInjection *FaultInjection `gorm:"foreignKey:TaskID;references:ID;constraint:OnDelete:CASCADE"`
	Execution      *Execution      `gorm:"foreignKey:TaskID;references:ID;constraint:OnDelete:CASCADE"`

	// One-to-many relationship with sub-tasks
	SubTasks []Task `gorm:"foreignKey:ParentTaskID;references:ID"`
}
