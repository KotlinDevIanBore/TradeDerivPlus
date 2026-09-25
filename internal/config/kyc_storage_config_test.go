package config

import (
	"strings"
	"testing"
	"time"
)

// populated is a Config carrying a complete object-storage setup, shaped the way
// Load returns one when all four R2 credential variables are present.
func populated() *Config {
	return &Config{
		R2AccountID:             "account",
		R2AccessKeyID:           "key",
		R2SecretAccessKey:       "secret",
		R2BucketName:            "bucket",
		KYCMaxDocumentBytes:     10 << 20,
		KYCUploadURLExpiry:      10 * time.Minute,
		KYCReviewURLExpiry:      5 * time.Minute,
		KYCAbandonedUploadAfter: 48 * time.Hour,
		KYCRetentionEvery:       time.Hour,
	}
}

// TestObjectStorageIsAllOrNone pins the rule the payout block established: a
// partial credential set is a misconfiguration and must fail at boot, not at the
// first upload. Absent entirely is legal.
//
// Load's validation is exercised through validateKYCStorage rather than Load
// itself, so the test does not have to stage two dozen unrelated environment
// variables to reach the four it cares about.
func TestObjectStorageIsAllOrNone(t *testing.T) {
	if err := validateKYCStorage(populated()); err != nil {
		t.Fatalf("a complete storage configuration was refused: %v", err)
	}

	empty := populated()
	empty.R2AccountID, empty.R2AccessKeyID = "", ""
	empty.R2SecretAccessKey, empty.R2BucketName = "", ""
	if err := validateKYCStorage(empty); err != nil {
		t.Fatalf("storage absent entirely should be legal, got: %v", err)
	}
}

// TestEveryStorageCredentialIsRequiredTogether blanks one credential at a time.
//
// Asserting only that a complete set passes would catch a dropped field but not
// one wired to the wrong source — both still pass with everything populated. This
// is the shape the B2C status-URL bug demanded: that bug was a value Load read
// correctly and a constructor silently dropped, invisible because nothing
// exercised the config-to-consumer path per field.
func TestEveryStorageCredentialIsRequiredTogether(t *testing.T) {
	cases := []struct {
		name  string
		blank func(*Config)
	}{
		{"R2_ACCOUNT_ID", func(c *Config) { c.R2AccountID = "" }},
		{"R2_ACCESS_KEY_ID", func(c *Config) { c.R2AccessKeyID = "" }},
		{"R2_SECRET_ACCESS_KEY", func(c *Config) { c.R2SecretAccessKey = "" }},
		{"R2_BUCKET_NAME", func(c *Config) { c.R2BucketName = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := populated()
			tc.blank(c)
			err := validateKYCStorage(c)
			if err == nil {
				t.Fatalf("%s was empty and the check passed anyway", tc.name)
			}
			// The message must name the missing variable, or an operator reading a
			// crash loop learns only that something is wrong.
			if !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("%s empty produced an error that does not name it: %v", tc.name, err)
			}
		})
	}
}

// TestRetentionZeroIsLegalAndNegativeIsNot guards the one config value that is
// deliberately unset.
//
// Zero means "never delete" and is the default, because the retention period for
// identity documents is a compliance decision (section 1 of
// docs/kyc-r2-upload-plan.md). A negative value is a typo, not a policy, and
// should not boot.
func TestRetentionZeroIsLegalAndNegativeIsNot(t *testing.T) {
	zero := populated()
	zero.KYCDocumentRetentionDays = 0
	if err := validateKYCStorage(zero); err != nil {
		t.Fatalf("retention 0 (never delete) was refused: %v", err)
	}

	negative := populated()
	negative.KYCDocumentRetentionDays = -1
	if err := validateKYCStorage(negative); err == nil {
		t.Fatal("a negative retention period was accepted")
	}
}

// TestKYCDurationsMustBePositive — a zero expiry would sign a URL that is already
// dead, and a zero sweep interval would spin.
func TestKYCDurationsMustBePositive(t *testing.T) {
	for _, tc := range []struct {
		name string
		zero func(*Config)
	}{
		{"KYC_MAX_DOCUMENT_BYTES", func(c *Config) { c.KYCMaxDocumentBytes = 0 }},
		{"KYC_UPLOAD_URL_EXPIRY", func(c *Config) { c.KYCUploadURLExpiry = 0 }},
		{"KYC_REVIEW_URL_EXPIRY", func(c *Config) { c.KYCReviewURLExpiry = 0 }},
		{"KYC_ABANDONED_UPLOAD_AFTER", func(c *Config) { c.KYCAbandonedUploadAfter = 0 }},
		{"KYC_RETENTION_EVERY", func(c *Config) { c.KYCRetentionEvery = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := populated()
			tc.zero(c)
			if err := validateKYCStorage(c); err == nil {
				t.Fatalf("%s at zero was accepted", tc.name)
			}
		})
	}
}
