import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, FileText, ShieldCheck, Upload, XCircle } from 'lucide-react'
import { ApiError } from '@/api/client'
import { kycApi, type UploadTarget } from '@/api/kyc'
import { Button } from '@/components/ui/Button'
import { Input } from '@/components/ui/Input'
import { Sheet } from '@/components/ui/Sheet'
import { Spinner } from '@/components/Spinner'
import { humanMessage } from '@/components/ErrorState'
import { queryKeys } from '@/lib/queryKeys'

type Step = 'form' | 'uploading' | 'submitted' | 'failed'

/** Kept in step with the server's allowlist; the server remains the boundary. */
const ACCEPT = 'image/jpeg,image/png,application/pdf'
const ALLOWED = ['image/jpeg', 'image/png', 'application/pdf']

/**
 * Identity verification: an ID number and one document.
 *
 * Three requests, in order: ask for a signed URL, PUT the file straight to the
 * object store, then confirm. The bytes never pass through our API.
 *
 * The client checks type and size before asking for a URL, which is a courtesy
 * that keeps a doomed 10 MB upload off a phone connection. It is not the
 * guarantee: the server checks the declared values, signs them into the URL, and
 * then checks what actually arrived — including sniffing the file's real type.
 *
 * `continuing` changes only the copy. A resumed attempt starts a completely fresh
 * upload — new URL, new key, the file chosen again — because resuming a byte
 * transfer against an expired signature is unreliable and buys little.
 */
export function VerificationSheet({
  open,
  onOpenChange,
  continuing = false,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** A previous attempt was never finished, so the copy says so. */
  continuing?: boolean
}) {
  const queryClient = useQueryClient()

  const [step, setStep] = useState<Step>('form')
  const [idNumber, setIdNumber] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [fieldError, setFieldError] = useState<{ id?: string; file?: string }>({})
  const [error, setError] = useState<string | null>(null)
  const [maxBytes, setMaxBytes] = useState(10 << 20)
  const fileInput = useRef<HTMLInputElement>(null)

  // A fresh sheet every time it opens: a half-filled form from a previous visit
  // is confusing, and a stale attempt id would be refused anyway.
  useEffect(() => {
    if (!open) return
    setStep('form')
    setIdNumber('')
    setFile(null)
    setFieldError({})
    setError(null)
  }, [open])

  function pick(next: File | null) {
    setFieldError((prev) => ({ ...prev, file: undefined }))
    if (!next) {
      setFile(null)
      return
    }
    if (!ALLOWED.includes(next.type)) {
      setFieldError((prev) => ({ ...prev, file: 'Choose a JPEG, PNG or PDF' }))
      setFile(null)
      return
    }
    if (next.size <= 0) {
      setFieldError((prev) => ({ ...prev, file: 'That file looks empty' }))
      setFile(null)
      return
    }
    if (next.size > maxBytes) {
      setFieldError((prev) => ({
        ...prev,
        file: `That file is too large. The limit is ${Math.floor(maxBytes / (1 << 20))} MB`,
      }))
      setFile(null)
      return
    }
    setFile(next)
  }

  async function submit() {
    const id = idNumber.trim()
    if (!id) {
      setFieldError((prev) => ({ ...prev, id: 'Enter your ID number' }))
      return
    }
    if (!file) {
      setFieldError((prev) => ({ ...prev, file: 'Choose a photo or PDF of your ID' }))
      return
    }

    setStep('uploading')
    setError(null)
    try {
      // The server re-checks the declared type and size and signs both into the
      // URL, so the client-side checks above are a courtesy, not the boundary.
      const target: UploadTarget = await kycApi.uploadURL(file.type, file.size)
      setMaxBytes(target.max_bytes)

      await kycApi.put(target, file)
      await kycApi.submit(target.attempt_id, id)

      // The withdraw gate and the account screen both read from these.
      queryClient.invalidateQueries({ queryKey: queryKeys.me() })
      queryClient.invalidateQueries({ queryKey: queryKeys.kycState() })
      setStep('submitted')
    } catch (e) {
      setError(e instanceof ApiError ? humanMessage(e) : e instanceof Error ? e.message : 'Something went wrong')
      setStep('failed')
      // A failed attempt is still an attempt: the account screen will offer to
      // continue it, so keep that state current.
      queryClient.invalidateQueries({ queryKey: queryKeys.kycState() })
    }
  }

  if (step === 'uploading') {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="Sending your document">
        <div className="py-8 text-center">
          <Spinner className="mx-auto h-8 w-8" />
          <p className="mt-4 text-body text-text">Sending your document</p>
          <p className="mx-auto mt-2 max-w-[34ch] text-meta text-muted">
            Keep this open until it finishes.
          </p>
        </div>
      </Sheet>
    )
  }

  if (step === 'submitted') {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="Document received">
        <div className="py-6 text-center">
          <CheckCircle2 className="mx-auto h-12 w-12 text-success" aria-hidden />
          <p className="mt-4 text-body text-text">We have your document</p>
          {/* No estimate: nothing in this system can promise when a person will
              look, and the deposit sheet's standard is to say what happens
              rather than when. */}
          <p className="mx-auto mt-2 max-w-[36ch] text-meta text-muted">
            Someone will review it and we will let you know. You do not need to send it again.
          </p>
          <Button block className="mt-6" onClick={() => onOpenChange(false)}>
            Done
          </Button>
        </div>
      </Sheet>
    )
  }

  if (step === 'failed') {
    return (
      <Sheet open={open} onOpenChange={onOpenChange} title="That did not work">
        <div className="py-6 text-center">
          <XCircle className="mx-auto h-12 w-12 text-danger" aria-hidden />
          <p className="mt-4 text-body text-text">{error ?? 'Something went wrong'}</p>
          <p className="mx-auto mt-2 max-w-[34ch] text-meta text-faint">
            Nothing was submitted. You can try again.
          </p>
          <Button block className="mt-6" onClick={() => setStep('form')}>
            Try again
          </Button>
          <Button variant="ghost" block className="mt-2" onClick={() => onOpenChange(false)}>
            Close
          </Button>
        </div>
      </Sheet>
    )
  }

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={continuing ? 'Finish verifying your identity' : 'Verify your identity'}
    >
      <div className="py-2">
        <div className="flex items-start gap-3">
          <ShieldCheck className="mt-0.5 h-5 w-5 shrink-0 text-accent" aria-hidden />
          <p className="text-meta text-muted">
            {continuing
              ? 'Your last upload did not finish. Choose the file again and we will send it.'
              : 'We need to confirm who you are before sending money out. This protects your account.'}
          </p>
        </div>

        <div className="mt-5 space-y-4">
          <Input
            label="ID number"
            value={idNumber}
            onChange={(e) => {
              setIdNumber(e.target.value)
              setFieldError((prev) => ({ ...prev, id: undefined }))
            }}
            error={fieldError.id}
            inputMode="numeric"
            autoComplete="off"
            maxLength={30}
            placeholder="As printed on your ID"
          />

          <div>
            <label className="mb-1.5 block text-meta text-muted" htmlFor="kyc-document">
              Photo of your ID, or a PDF
            </label>
            <input
              ref={fileInput}
              id="kyc-document"
              type="file"
              accept={ACCEPT}
              className="sr-only"
              onChange={(e) => pick(e.target.files?.[0] ?? null)}
            />
            <Button
              type="button"
              variant="outline"
              block
              onClick={() => fileInput.current?.click()}
            >
              {file ? <FileText className="h-4 w-4" aria-hidden /> : <Upload className="h-4 w-4" aria-hidden />}
              {file ? 'Choose a different file' : 'Choose a file'}
            </Button>
            {file ? (
              <p className="mt-2 truncate text-meta text-muted" title={file.name}>
                {file.name} · {Math.max(1, Math.round(file.size / 1024))} KB
              </p>
            ) : (
              <p className="mt-2 text-meta text-faint">
                JPEG, PNG or PDF, up to {Math.floor(maxBytes / (1 << 20))} MB
              </p>
            )}
            {fieldError.file ? <p className="mt-1 text-meta text-danger">{fieldError.file}</p> : null}
          </div>
        </div>

        <Button block className="mt-6" onClick={submit} disabled={!file || !idNumber.trim()}>
          Send for review
        </Button>
      </div>
    </Sheet>
  )
}
