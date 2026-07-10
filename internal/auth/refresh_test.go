package auth

import "testing"

func TestGenerateRefreshToken_UniqueAndHashMatches(t *testing.T) {
	plainA, hashA, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}
	plainB, hashB, err := GenerateRefreshToken()
	if err != nil {
		t.Fatalf("GenerateRefreshToken: %v", err)
	}

	if plainA == plainB {
		t.Fatal("two generated refresh tokens were identical")
	}
	if hashA == hashB {
		t.Fatal("two generated refresh token hashes were identical")
	}

	if got := HashRefreshToken(plainA); got != hashA {
		t.Errorf("HashRefreshToken(plainA) = %q, want %q (the hash returned at generation)", got, hashA)
	}
	if got := HashRefreshToken(plainB); got != hashB {
		t.Errorf("HashRefreshToken(plainB) = %q, want %q (the hash returned at generation)", got, hashB)
	}
}

func TestHashRefreshToken_Deterministic(t *testing.T) {
	const plaintext = "some-fixed-refresh-token-value"
	first := HashRefreshToken(plaintext)
	second := HashRefreshToken(plaintext)
	if first != second {
		t.Fatalf("HashRefreshToken produced different output for the same input: %q vs %q", first, second)
	}
}
