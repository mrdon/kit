import { useState } from 'react';
import { scanReceipt, submitIntake, type ScanResult } from './api';

const CURRENCIES = ['USD', 'EUR', 'GBP', 'CAD', 'AUD'];

type Step = 'upload' | 'form' | 'done';

interface Fields {
  attachment_id: string;
  vendor: string;
  spent_on: string;
  amount: string;
  tax: string;
  currency: string;
  category: string;
  purpose: string;
  name: string;
  email: string;
}

const emptyFields: Fields = {
  attachment_id: '',
  vendor: '',
  spent_on: '',
  amount: '',
  tax: '',
  currency: 'USD',
  category: '',
  purpose: '',
  name: '',
  email: '',
};

// Public, no-login expense intake: upload a receipt, correct the OCR-prefilled
// fields, add name + email, submit. The report lands in the approval workflow.
export default function Intake() {
  const [step, setStep] = useState<Step>('upload');
  const [fields, setFields] = useState<Fields>(emptyFields);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [website, setWebsite] = useState(''); // honeypot
  const [reportId, setReportId] = useState('');

  const set = (k: keyof Fields, v: string) =>
    setFields((f) => ({ ...f, [k]: v }));

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setBusy(true);
    setErr(null);
    try {
      const r: ScanResult = await scanReceipt(file);
      setFields({
        ...emptyFields,
        attachment_id: r.attachment_id,
        vendor: r.vendor,
        spent_on: r.spent_on,
        amount: r.amount,
        tax: r.tax,
        currency: r.currency || 'USD',
      });
      setStep('form');
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    try {
      const res = await submitIntake({ ...fields, website });
      setReportId(res.report_id);
      setStep('done');
    } catch (e) {
      setErr((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="page" style={{ maxWidth: '32rem', margin: '0 auto' }}>
      <div className="page-head">
        <h1>Submit an expense</h1>
        <p className="page-sub">
          Upload a receipt and we’ll read the details for you to check. Add your
          email so we know who to reimburse — someone will review and approve it.
        </p>
      </div>

      {err && <p className="banner banner-error">{err}</p>}

      {step === 'upload' && (
        <label className="field">
          <span>Receipt photo or PDF</span>
          <input
            type="file"
            accept="image/*,application/pdf"
            disabled={busy}
            onChange={onFile}
          />
          {busy && <p className="page-sub">Reading your receipt…</p>}
        </label>
      )}

      {step === 'form' && (
        <form onSubmit={submit} className="stack-form">
          <label className="field">
            <span>Vendor</span>
            <input
              value={fields.vendor}
              onChange={(e) => set('vendor', e.target.value)}
              placeholder="Where you spent it"
            />
          </label>
          <div style={{ display: 'flex', gap: '0.75rem' }}>
            <label className="field" style={{ flex: 1 }}>
              <span>Amount</span>
              <input
                value={fields.amount}
                onChange={(e) => set('amount', e.target.value)}
                placeholder="12.34"
                inputMode="decimal"
                required
              />
            </label>
            <label className="field" style={{ width: '8rem' }}>
              <span>Currency</span>
              <select
                value={fields.currency}
                onChange={(e) => set('currency', e.target.value)}
              >
                {CURRENCIES.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div style={{ display: 'flex', gap: '0.75rem' }}>
            <label className="field" style={{ flex: 1 }}>
              <span>Date</span>
              <input
                type="date"
                value={fields.spent_on}
                onChange={(e) => set('spent_on', e.target.value)}
              />
            </label>
            <label className="field" style={{ flex: 1 }}>
              <span>Tax (optional)</span>
              <input
                value={fields.tax}
                onChange={(e) => set('tax', e.target.value)}
                placeholder="0.00"
                inputMode="decimal"
              />
            </label>
          </div>
          <label className="field">
            <span>What was it for? (optional)</span>
            <input
              value={fields.purpose}
              onChange={(e) => set('purpose', e.target.value)}
              placeholder="e.g. snacks for the volunteer event"
            />
          </label>
          <label className="field">
            <span>Your name</span>
            <input
              value={fields.name}
              onChange={(e) => set('name', e.target.value)}
            />
          </label>
          <label className="field">
            <span>Your email</span>
            <input
              type="email"
              value={fields.email}
              onChange={(e) => set('email', e.target.value)}
              required
            />
          </label>
          {/* Honeypot: hidden from people, tempting to bots. */}
          <input
            type="text"
            name="website"
            tabIndex={-1}
            autoComplete="off"
            value={website}
            onChange={(e) => setWebsite(e.target.value)}
            style={{ position: 'absolute', left: '-9999px' }}
            aria-hidden="true"
          />
          <div className="drawer-actions">
            <button className="btn" type="submit" disabled={busy}>
              {busy ? 'Submitting…' : 'Submit for approval'}
            </button>
          </div>
        </form>
      )}

      {step === 'done' && (
        <div>
          <p className="banner banner-ok">
            Thanks! Your expense was submitted for approval.
          </p>
          <p className="page-sub">
            Reference: <code>{reportId.slice(0, 8)}</code>. Someone will review
            it shortly.
          </p>
          <div className="drawer-actions">
            <button
              className="btn"
              type="button"
              onClick={() => {
                setFields(emptyFields);
                setWebsite('');
                setReportId('');
                setStep('upload');
              }}
            >
              Submit another
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
