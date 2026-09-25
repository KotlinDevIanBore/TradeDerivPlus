import { api } from './client'
import type { KYCStatus } from '@/types'

export interface UploadTarget {
  attempt_id: string
  object_key: string
  upload: {
    url: string
    method: string
    /** Headers the signature covers. Every one must be replayed verbatim. */
    headers: Record<string, string[]>
    expires_at: string
  }
  max_bytes: number
  allowed_content_types: string[]
}

export interface VerificationState {
  status: KYCStatus
  /** An upload was started and never confirmed, so offer "continue". */
  unfinished_upload: boolean
}

export interface KYCSubmissionResult {
  id: string
  status: string
  submitted_at: string
}

/**
 * Identity verification, in three steps: ask for somewhere to put the document,
 * put it there, then say it is there.
 *
 * The middle step deliberately does NOT go through `api`. That wrapper
 * JSON-stringifies every body, attaches the bearer token and sends cookies
 * (`client.ts`), none of which belongs on a request to the object store: the body
 * is raw bytes, our token must not be sent to a third party, and the response has
 * no JSON to parse. So the upload is a bare fetch, and only the first and third
 * steps are API calls.
 */
export const kycApi = {
  state: () => api.get<VerificationState>('/kyc/state'),

  uploadURL: (contentType: string, sizeBytes: number) =>
    api.post<UploadTarget>('/kyc/upload-url', {
      content_type: contentType,
      size_bytes: sizeBytes,
    }),

  submit: (attemptID: string, idNumber: string) =>
    api.post<KYCSubmissionResult>('/kyc/submit', {
      attempt_id: attemptID,
      id_number: idNumber,
    }),

  /**
   * Upload the bytes to the signed URL.
   *
   * Replays exactly the headers the server signed. Content-Length is set by the
   * browser from the body and cannot be assigned from script, so it is skipped —
   * the value the browser computes is the file's real length, which is what was
   * signed.
   */
  async put(target: UploadTarget, file: File): Promise<void> {
    const headers: Record<string, string> = {}
    for (const [name, values] of Object.entries(target.upload.headers ?? {})) {
      if (name.toLowerCase() === 'content-length') continue
      if (values?.length) headers[name] = values.join(', ')
    }

    let res: Response
    try {
      res = await fetch(target.upload.url, {
        method: target.upload.method || 'PUT',
        headers,
        body: file,
        // No cookies to a third-party host, whatever the browser's default.
        credentials: 'omit',
      })
    } catch {
      throw new Error('We could not reach the upload service. Check your connection and try again.')
    }
    if (!res.ok) {
      // The store answers with XML, which is no use to anyone reading a sheet.
      // A signature mismatch (403) almost always means the file changed between
      // choosing it and sending it.
      if (res.status === 403) {
        throw new Error('That upload link is no longer valid. Try again.')
      }
      throw new Error('The upload did not complete. Try again.')
    }
  },
}
