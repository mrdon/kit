// The admin's own instruction for poster and template chat. It is added to
// Kit's built-in rules, so it can shift style and brand defaults but never
// lift the hard limits (library photos and the logo are the only pictures).

export const MAX_GUIDANCE = 4000;

export default function PosterGuidanceField({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  return (
    <section className="panel">
      <h2 className="panel-title">Poster guidance</h2>
      <label className="field">
        <span>Extra instructions for poster chat (optional)</span>
        <textarea
          rows={5}
          maxLength={MAX_GUIDANCE}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="e.g. Favour bold geometric shapes over thin rules. A soft fade over the photo is fine on event posters."
        />
        <span className="field-note">
          Added to Kit's built-in poster rules. The brand guide stays the default and anyone can ask for
          something off-brand in chat; this sets the house style. Kit never uses generated images: only
          library photos, stock (if on) and the logo.
        </span>
      </label>
    </section>
  );
}
