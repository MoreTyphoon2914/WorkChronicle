package workpolicy

// Evaluator derives business-facing target status from classified work. A
// future adaptive implementation can satisfy this interface without changing
// timeline generation or report models.
type Evaluator interface {
	Evaluate(summary WorkSummary) Evaluation
}
