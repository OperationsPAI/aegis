// Package framework defines the self-registration plugin points that let a
// new AegisLab module be added without editing centralized aggregation files
// (issue #28: "加一个模块，不用改别的模块的源代码").
//
// Each plugin point is exposed as a named fx value-group. A module
// contributes by `fx.Provide(...)` a constructor with
// `fx.ResultTags(`group:"..."`)`, and the framework aggregates all
// contributions at startup.
//
// The five plugin points:
//
//  1. Routes          — group:"routes"
//  2. Permissions     — group:"permissions"
//  3. Role grants     — group:"role_grants"
//  4. Migrations      — group:"migrations"
//  5. Task executors  — group:"task_executors"
//
// Middleware is deliberately NOT a plugin point: it is global policy.
package framework
