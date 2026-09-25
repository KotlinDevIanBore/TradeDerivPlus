package service

import "github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"

// Customer-facing copy for a failed payout.
//
// This is the payout counterpart of humanResult, and it is deliberately a
// SEPARATE table rather than a branch inside it. The two vocabularies share
// several numbers and agree about none of them:
//
//	code  STK push / query (humanResult)        B2C payout (here)
//	1     the customer's M-Pesa balance is low  OUR utility float is empty
//	2001  the customer entered a wrong PIN      our initiator credential is wrong
//
// So a payout failure rendered through humanResult would tell a customer their
// own balance was too low when ours was, and that they mistyped a PIN they were
// never asked for. Both are false, and the second is the kind of false that makes
// a support conversation worse.
//
// The other half of the split is mpesaclient.PayoutFailureCodes, which answers
// "is this final" — a code becomes terminal there whether or not anyone has
// written copy for it here, and an unrecognised code falls through to a sentence
// that is true of every one of them.
//
// The house rule these follow: say what happened and what the customer should do,
// never leak Daraja's own words, and never imply the customer did something wrong
// when the fault is ours.
func humanPayoutResult(code, resultDesc string) string {
	switch code {
	// The customer's, and actionable by them.
	case "2040":
		return "That number is not registered on M-Pesa. Check it and try again"
	case "SFC_IC0003":
		return "That number doesn't exist on M-Pesa. Check it and try again"
	case "8":
		return "M-Pesa won't accept this amount into that account right now. Try a smaller amount"
	case "2":
		return "That amount is below M-Pesa's minimum for a payout. Try a larger amount"
	case "3":
		return "That amount is above M-Pesa's limit for one payout. Try a smaller amount"
	case "4":
		return "This would pass M-Pesa's daily limit for your number. Try again tomorrow, or withdraw less"

	// Ours. The customer did nothing wrong and is told so, without being told
	// which of our problems it is.
	case "1", "11", "21", "2001", "2006", "2028", "8006":
		return "We couldn't send this payout. Nothing was taken from your balance — we're on it, and you can try again shortly"
	}

	// Unrecognised. True of every failure: the money is back, and this is not
	// their fault as far as we can tell.
	if resultDesc != "" {
		// Deliberately not interpolated into the sentence. Daraja's descriptions
		// are written for operators ("DebitParty in invalid state"), so the text
		// is kept for the ledger and the log, not shown.
		_ = resultDesc
	}
	return "We couldn't send this payout. Your balance is unchanged — please try again, or contact support if it keeps happening"
}

// payoutFailureIsOurs reports whether a code means the business must fix
// something before the next payout can work. Delegates rather than keeping a
// second list, so the two cannot drift.
func payoutFailureIsOurs(code string) bool { return mpesaclient.PayoutOursCodes[code] }
