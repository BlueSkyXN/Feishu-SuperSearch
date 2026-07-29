package apiassets

import _ "embed"

// retrievalPlanSchema is the canonical runtime contract for RetrievalPlan v1.
// Keeping the schema embedded makes validation independent of the process
// working directory and prevents a caller from replacing the contract on disk.
//
//go:embed retrieval-plan.schema.json
var retrievalPlanSchema []byte

// RetrievalPlanSchema returns an isolated copy of the embedded schema.
func RetrievalPlanSchema() []byte {
	return append([]byte(nil), retrievalPlanSchema...)
}
