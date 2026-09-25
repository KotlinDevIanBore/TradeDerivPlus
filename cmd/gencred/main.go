// Command gencred turns an M-PESA initiator password into the
// MPESA_SECURITY_CREDENTIAL value Daraja's payout endpoints expect.
//
// Run it once, on a machine you trust, and copy the printed value into the
// deployment's environment. Re-run it when the initiator password is rotated;
// the certificate is stable across rotations.
//
//	export MPESA_INITIATOR_PASSWORD='...'
//	go run ./cmd/gencred -cert path/to/mpesa_production.cer
//
// The password is read from the environment and never from a flag, so it does
// not land in shell history or in the process list. Nothing is written to disk
// and nothing but the credential is printed — so the output can be piped, and a
// mistake cannot leak the password into a log.
//
// Which certificate: sandbox and production differ, and using the wrong one
// yields a credential Daraja rejects as 2001, the same code as a wrong password.
// Safaricom publishes both; the sandbox certificate also ships in Safaricom's own
// node library at keys/sandbox-cert.cer. No certificate is committed to this
// repository — it is an input, and which one is right depends on where you are
// pointing.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"

	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

func main() {
	cert := flag.String("cert", "", "path to the Safaricom certificate (.cer) to encrypt under")
	flag.Parse()

	if *cert == "" {
		fail("-cert is required: the path to the Safaricom certificate for the environment you are targeting")
	}

	// Best-effort, so the password can live in .env rather than in a shell.
	_ = godotenv.Load()

	password := strings.TrimSpace(os.Getenv("MPESA_INITIATOR_PASSWORD"))
	if password == "" {
		fail("MPESA_INITIATOR_PASSWORD must be set (in the environment or .env)")
	}

	pemBytes, err := os.ReadFile(*cert)
	if err != nil {
		fail(fmt.Sprintf("read certificate: %v", err))
	}

	credential, err := mpesaclient.EncryptInitiatorPassword(password, pemBytes)
	if err != nil {
		fail(err.Error())
	}

	// Only the credential, on stdout. Everything else goes to stderr so this can
	// be piped straight into a secret store.
	fmt.Println(credential)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "gencred: "+msg)
	os.Exit(1)
}
