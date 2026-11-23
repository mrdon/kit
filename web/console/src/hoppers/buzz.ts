// buzz vibrates the phone, where it can. A phone in a pocket notices a buzz
// when its turn comes up; one with no motor (or no permission) just doesn't.
export function buzz(p: number | number[]) {
  try {
    navigator.vibrate?.(p);
  } catch {
    /* no vibration motor, or not allowed */
  }
}
