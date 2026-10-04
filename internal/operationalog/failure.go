package operationalog

// FailurePhase defines the finite diagnostic vocabulary shared by access logs
// and their safe API projection. It never inspects an error message.
func FailurePhase(code string) string {
	switch code {
	case "upstream_configuration_invalid", "upstream_policy_rejected":
		return "prepare"
	case "upstream_egress_failed":
		return "egress"
	case "upstream_transport_failed", "upstream_status_rejected":
		return "fetch"
	case "upstream_body_failed":
		return "body"
	case "unknown":
		return "unknown"
	default:
		return ""
	}
}
