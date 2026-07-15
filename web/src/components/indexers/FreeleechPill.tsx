// Freeleech-only badge; shown next to TypePill when the instance's freeleech
// checkbox is enabled (autobrr/harbrr#188). Styled after TypePill's tinted pill.
export function FreeleechPill({ freeleech }: { freeleech?: boolean }) {
  if (!freeleech) return null

  return (
    <span className="inline-flex items-center rounded-full border border-ok/40 bg-ok/10 px-2 py-0.5 text-[11px] font-medium text-ok">
      Freeleech
    </span>
  )
}
