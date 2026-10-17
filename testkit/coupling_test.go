package testkit

import "testing"

func TestNoProductVocabularyInPlatformService(t *testing.T) {
	CheckNoProductVocabulary(t, "..")
}
