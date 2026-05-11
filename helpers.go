package workcellcomponents

// asFloat coerces an interface{} from a DoCommand args map into a
// float64, handling the typical JSON numeric shapes that arrive at the
// gRPC boundary. Returns 0 for any non-numeric input.
func asFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
