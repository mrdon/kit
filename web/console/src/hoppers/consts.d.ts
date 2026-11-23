// Types for consts.js, which stays plain JS so the TV can inline it.
export declare const HOP: {
  readonly phase: { readonly lobby: 'lobby'; readonly racing: 'racing'; readonly final: 'final' };
  readonly sub: { readonly ready: 'ready'; readonly flick: 'flick'; readonly hop: 'hop'; readonly raceEnd: 'raceEnd' };
  readonly cmd: { readonly join: 'join'; readonly seen: 'seen'; readonly leave: 'leave'; readonly flick: 'flick'; readonly host: 'host' };
  readonly heat: { readonly waiting: 'waiting'; readonly next: 'next'; readonly racing: 'racing'; readonly done: 'done' };
  readonly result: { readonly place: 'place' };
  readonly spot: { readonly launch: 'launch'; readonly chute: 'chute' };
  readonly layout: { readonly straight: 'straight'; readonly switchback: 'switchback' };
  readonly msg: { readonly state: 'state' };
  readonly role: { readonly tv: 'tv'; readonly player: 'player'; readonly host: 'host' };
  readonly flickBand: number;
};
