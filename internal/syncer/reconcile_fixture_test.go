package syncer

func containsPath(values []string, p string) bool {
	for _, v := range values {
		if v == p {
			return true
		}
	}
	return false
}
