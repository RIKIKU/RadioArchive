// The page's one player (ADR 0012), made by the layout, for the pages that
// play through it: the Recent calls page replays a call in the dock (R46).
import { getContext, setContext } from 'svelte';
import type { Player } from './player.svelte.ts';

const key = Symbol('player');

/** The layout hands its player to the pages under it. */
export const providePlayer = (p: Player): Player => setContext(key, p);

/** The layout's player. */
export const usePlayer = (): Player => getContext<Player>(key);
