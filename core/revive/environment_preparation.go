package revive

// Execution facts are still derived on the server. AllowUntested is only the
// specific profile exception already exposed by --allow-draft-profile; it is
// not evidence of a successful check or a general execution authorization.
type EnvironmentPreparationParams struct {
	EnvironmentPlanParams
	OperationID   string `json:"operation_id"`
	PreviewDigest string `json:"preview_digest"`
	AllowUntested bool   `json:"allow_untested"`
}
