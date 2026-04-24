package validate

// Verdict composition lives in validate.go alongside the pipeline orchestra.
// This file is reserved for helpers dedicated to stage-5 behavior so the
// precedence contract stays easy to audit.
//
// Precedence, from strongest to weakest:
//   refuse > accept_with_warning > accept
//
// Strict mode: when a prior stage emitted warnings, Pipeline.Validate
// promotes the composite result to refuse with FailedStage=StageVerdict
// and RefusalReason=ReasonStrictWarningUpgrade. See SPECS §8.
