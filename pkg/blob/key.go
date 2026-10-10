package blob

import "regexp"

var validKey = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (k Key) Valid() bool { return validKey.MatchString(string(k)) }
