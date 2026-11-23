// Types for the parts of frog.js the phone uses. The file stays plain JS so
// the TV can inline it.
export declare const COLORS: readonly string[];
export declare const HATS: readonly string[];
export declare function hatSVG(hat: string, color: string): string;
export declare function frogSVG(color: string, hat: string, opts?: { mood?: 'wow'; badge?: boolean }): string;
