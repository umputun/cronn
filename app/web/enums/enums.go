// Package enums provides type-safe enumeration types for the web interface.
//
// This package uses code generation via go-pkgz/enum to create robust enum types
// with automatic string conversion, database marshaling, and parsing capabilities.
//
// Code Generation:
//
// The enum types are defined as unexported integer types (e.g., jobStatus int) in this file,
// and the go:generate directives invoke the enum generator to create corresponding exported
// types with all necessary methods in separate files (*_enum.go).
//
// For each enum type, the generator creates:
//   - An exported struct type (e.g., JobStatus) with name and value fields
//   - String() method for string representation
//   - Parse functions (e.g., ParseJobStatus) for string-to-enum conversion
//   - Database methods (Scan/Value) for SQL compatibility
//   - JSON marshaling methods (MarshalText/UnmarshalText)
//   - Exported constants for each enum value (e.g., JobStatusIdle, JobStatusRunning)
//
// Usage:
//
//	// Use the generated constants
//	status := enums.JobStatusRunning
//
//	// Convert to string for display
//	fmt.Println(status.String()) // "running"
//
//	// Parse from string (e.g., from user input)
//	parsed, err := enums.ParseJobStatus("success")
//	if err != nil {
//	    // handle invalid input
//	}
//
//	// Database storage works automatically
//	// Enums are stored as strings and converted transparently
//
// To regenerate the enum types after modifications:
//
//	go generate ./app/web/enums
//
// Note: The unexported type definitions below are only used by the generator.
// All actual code should use the generated exported types.
package enums

//go:generate go run github.com/go-pkgz/enum@latest -type jobStatus -lower
//go:generate go run github.com/go-pkgz/enum@latest -type eventType -lower
//go:generate go run github.com/go-pkgz/enum@latest -type viewMode -lower
//go:generate go run github.com/go-pkgz/enum@latest -type theme -lower
//go:generate go run github.com/go-pkgz/enum@latest -type sortMode -lower
//go:generate go run github.com/go-pkgz/enum@latest -type filterMode -lower

// jobStatus represents the status of a job.
// This is an unexported type used only as input for the code generator.
// Use the exported JobStatus type and its constants in actual code.
type jobStatus int

const (
	jobStatusIdle jobStatus = iota
	jobStatusRunning
	jobStatusSuccess
	jobStatusFailed
)

// eventType represents job event types.
// This is an unexported type used only as input for the code generator.
// Use the exported EventType type and its constants in actual code.
type eventType int

const (
	eventTypeStarted eventType = iota
	eventTypeCompleted
	eventTypeFailed
)

// viewMode represents UI view modes.
// This is an unexported type used only as input for the code generator.
// Use the exported ViewMode type and its constants in actual code.
type viewMode int

const (
	viewModeCards viewMode = iota
	viewModeList
)

// theme represents UI themes.
// This is an unexported type used only as input for the code generator.
// Use the exported Theme type and its constants in actual code.
type theme int

const (
	themeLight theme = iota
	themeDark
	themeAuto
)

// sortMode represents job sorting modes.
// This is an unexported type used only as input for the code generator.
// Use the exported SortMode type and its constants in actual code.
type sortMode int

const (
	sortModeDefault sortMode = iota
	sortModeLastrun
	sortModeNextrun
)

// filterMode represents job filtering modes by status.
// This is an unexported type used only as input for the code generator.
// Use the exported FilterMode type and its constants in actual code.
type filterMode int

const (
	filterModeAll filterMode = iota
	filterModeRunning
	filterModeSuccess
	filterModeFailed
)
