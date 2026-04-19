package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const (
	// Salt length in bytes, used for legacy SHA-256 hashes only.
	SaltLength = 32
	// Minimum password length
	MinPasswordLength = 8
	// Maximum password length
	MaxPasswordLength = 128

	// BcryptCost is the work factor for new bcrypt hashes. 12 balances the
	// OWASP 2024 guidance (10–14) against our synchronous login path; drop
	// to 10 locally if CI starts timing out on hashing.
	BcryptCost = 12

	// bcryptHashPrefix marks a password record as bcrypt-encoded. Storage
	// format: "bcrypt$" + bcrypt.GenerateFromPassword(...) output (≤60 bytes
	// of ASCII). The User.Password column is size:255 (see model/entity.go)
	// so a 67-byte prefixed hash fits with plenty of headroom.
	bcryptHashPrefix = "bcrypt$"
)

// PasswordStrength represents password strength levels
type PasswordStrength int

const (
	WeakPassword PasswordStrength = iota
	ModeratePassword
	StrongPassword
	VeryStrongPassword
)

// HashPassword creates a bcrypt hash of the password, prefixed with
// "bcrypt$" so VerifyPassword can distinguish it from legacy salted-SHA-256
// records. Returns an error if the password fails basic length validation.
//
// New records (registration, change-password, API-key secret hashing) all
// go through this path and therefore land in the bcrypt regime.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", fmt.Errorf("password must be at least %d characters long", MinPasswordLength)
	}

	if len(password) > MaxPasswordLength {
		return "", fmt.Errorf("password must be no more than %d characters long", MaxPasswordLength)
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("failed to bcrypt password: %w", err)
	}
	return bcryptHashPrefix + string(hashed), nil
}

// VerifyPassword checks the supplied plaintext password against a stored
// hash. Records with the "bcrypt$" prefix are verified via bcrypt; anything
// else is treated as a legacy salted-SHA-256 record and verified with the
// pre-rotation code. The caller is responsible for opportunistically
// re-hashing successful legacy verifications (see NeedsRehash /
// RehashPassword) to migrate the database forward.
func VerifyPassword(password, hashedPassword string) bool {
	if strings.HasPrefix(hashedPassword, bcryptHashPrefix) {
		err := bcrypt.CompareHashAndPassword(
			[]byte(hashedPassword[len(bcryptHashPrefix):]),
			[]byte(password),
		)
		return err == nil
	}
	return verifyLegacySHA256(password, hashedPassword)
}

// NeedsRehash reports whether a stored hash is in the legacy SHA-256 format
// and should be transparently upgraded to bcrypt on the next successful
// VerifyPassword. Returns false for bcrypt records and for anything that
// fails to parse at all (the caller should then surface an auth failure).
func NeedsRehash(hashedPassword string) bool {
	if hashedPassword == "" {
		return false
	}
	if strings.HasPrefix(hashedPassword, bcryptHashPrefix) {
		return false
	}
	// Legacy format is "<saltHex>:<hashHex>"; anything else is malformed.
	parts := strings.Split(hashedPassword, ":")
	return len(parts) == 2
}

// verifyLegacySHA256 reproduces the pre-rotation salted-SHA-256 verification
// path. Kept so existing user records continue to authenticate until they
// are opportunistically migrated to bcrypt.
func verifyLegacySHA256(password, hashedPassword string) bool {
	parts := strings.Split(hashedPassword, ":")
	if len(parts) != 2 {
		return false
	}

	saltHex := parts[0]
	expectedHashHex := parts[1]

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false
	}

	hash := sha256.New()
	hash.Write(salt)
	hash.Write([]byte(password))
	actualHashHex := hex.EncodeToString(hash.Sum(nil))

	return constantTimeCompare(expectedHashHex, actualHashHex)
}

// constantTimeCompare performs constant-time string comparison to prevent timing attacks
func constantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}

	result := 0
	for i := 0; i < len(a); i++ {
		result |= int(a[i]) ^ int(b[i])
	}

	return result == 0
}

// ValidatePasswordStrength checks password strength and returns suggestions
func ValidatePasswordStrength(password string) (PasswordStrength, []string, error) {
	if len(password) < MinPasswordLength {
		return WeakPassword, []string{"Password must be at least 8 characters long"},
			errors.New("password too short")
	}

	if len(password) > MaxPasswordLength {
		return WeakPassword, []string{"Password must be no more than 128 characters long"},
			errors.New("password too long")
	}

	var suggestions []string
	score := 0

	// Check length
	if len(password) >= 8 {
		score++
	}
	if len(password) >= 12 {
		score++
	}

	// Check for lowercase letters
	hasLower := false
	for _, char := range password {
		if char >= 'a' && char <= 'z' {
			hasLower = true
			break
		}
	}
	if hasLower {
		score++
	} else {
		suggestions = append(suggestions, "Add lowercase letters")
	}

	// Check for uppercase letters
	hasUpper := false
	for _, char := range password {
		if char >= 'A' && char <= 'Z' {
			hasUpper = true
			break
		}
	}
	if hasUpper {
		score++
	} else {
		suggestions = append(suggestions, "Add uppercase letters")
	}

	// Check for digits
	hasDigit := false
	for _, char := range password {
		if char >= '0' && char <= '9' {
			hasDigit = true
			break
		}
	}
	if hasDigit {
		score++
	} else {
		suggestions = append(suggestions, "Add numbers")
	}

	// Check for special characters
	hasSpecial := false
	specialChars := "!@#$%^&*()_+-=[]{}|;:,.<>?"
	for _, char := range password {
		for _, special := range specialChars {
			if char == special {
				hasSpecial = true
				break
			}
		}
		if hasSpecial {
			break
		}
	}
	if hasSpecial {
		score++
	} else {
		suggestions = append(suggestions, "Add special characters (!@#$%^&*)")
	}

	// Check for common patterns (simple check)
	commonPatterns := []string{
		"123456", "password", "admin", "qwerty", "abc123",
		"111111", "123123", "password123", "admin123",
	}
	for _, pattern := range commonPatterns {
		if strings.Contains(strings.ToLower(password), pattern) {
			suggestions = append(suggestions, "Avoid common patterns like '"+pattern+"'")
			score-- // Penalty for common patterns
			break
		}
	}

	// Determine strength based on score
	var strength PasswordStrength
	switch {
	case score >= 6:
		strength = VeryStrongPassword
	case score >= 4:
		strength = StrongPassword
	case score >= 2:
		strength = ModeratePassword
	default:
		strength = WeakPassword
	}

	if len(suggestions) == 0 && strength >= StrongPassword {
		suggestions = append(suggestions, "Password strength is good!")
	}

	return strength, suggestions, nil
}

// PasswordStrengthString returns a string representation of password strength
func (ps PasswordStrength) String() string {
	switch ps {
	case WeakPassword:
		return "Weak"
	case ModeratePassword:
		return "Moderate"
	case StrongPassword:
		return "Strong"
	case VeryStrongPassword:
		return "Very Strong"
	default:
		return "Unknown"
	}
}

// GenerateRandomPassword generates a random password with specified length
func GenerateRandomPassword(length int) (string, error) {
	if length < MinPasswordLength {
		length = MinPasswordLength
	}
	if length > MaxPasswordLength {
		length = MaxPasswordLength
	}

	// Character sets
	lowercase := "abcdefghijklmnopqrstuvwxyz"
	uppercase := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digits := "0123456789"
	special := "!@#$%^&*()_+-=[]{}|;:,.<>?"

	allChars := lowercase + uppercase + digits + special

	// Ensure at least one character from each set
	password := make([]byte, length)

	// First 4 characters: one from each set
	charSets := []string{lowercase, uppercase, digits, special}
	for i := 0; i < 4 && i < length; i++ {
		set := charSets[i]
		randomIndex := make([]byte, 1)
		if _, err := rand.Read(randomIndex); err != nil {
			return "", fmt.Errorf("failed to generate random password: %v", err)
		}
		password[i] = set[int(randomIndex[0])%len(set)]
	}

	// Fill remaining positions with random characters
	for i := 4; i < length; i++ {
		randomIndex := make([]byte, 1)
		if _, err := rand.Read(randomIndex); err != nil {
			return "", fmt.Errorf("failed to generate random password: %v", err)
		}
		password[i] = allChars[int(randomIndex[0])%len(allChars)]
	}

	// Shuffle the password to avoid predictable patterns
	for i := length - 1; i > 0; i-- {
		randomIndex := make([]byte, 1)
		if _, err := rand.Read(randomIndex); err != nil {
			return "", fmt.Errorf("failed to shuffle password: %v", err)
		}
		j := int(randomIndex[0]) % (i + 1)
		password[i], password[j] = password[j], password[i]
	}

	return string(password), nil
}

// IsPasswordValid performs basic password validation
func IsPasswordValid(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters long", MinPasswordLength)
	}

	if len(password) > MaxPasswordLength {
		return fmt.Errorf("password must be no more than %d characters long", MaxPasswordLength)
	}

	// Check for at least one letter and one number
	hasLetter := false
	hasNumber := false

	for _, char := range password {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') {
			hasLetter = true
		}
		if char >= '0' && char <= '9' {
			hasNumber = true
		}
	}

	if !hasLetter {
		return errors.New("password must contain at least one letter")
	}

	if !hasNumber {
		return errors.New("password must contain at least one number")
	}

	return nil
}
