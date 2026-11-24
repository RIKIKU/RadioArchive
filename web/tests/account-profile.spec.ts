// What a browser does at a listener's first sign-in (R41, ADR 0009), on its
// own, with no browser: web/src/lib/account/profile.ts. The whole flow, with
// Authentik, is web/signin-tests/a33.spec.ts.
import { test, expect } from '@playwright/test';
import { firstStep, profileOf, sameProfile, type Profile } from '../src/lib/account/profile.ts';

test.beforeEach(() => test.skip(test.info().project.name !== 'chromium', 'no browser: once is enough'));

const browser = profileOf([{ id: 1001 }], 'harbour', 'system', false);
const account = (p: Partial<Profile>): Profile => ({ v: 1, talkgroups: [], alerts: false, ...p });

test('first sign-in: an account with no set gets the browser’s', () => {
	expect(firstStep(false, null, browser)).toBe('save');
	expect(firstStep(false, account({ look: 'console' }), browser)).toBe('save');
});

test('first sign-in: an account with another set asks once; with the same, nothing is asked', () => {
	expect(firstStep(false, account({ talkgroups: [{ id: 1002 }] }), browser)).toBe('ask');
	// The marks count, and so do the settings that go with the set.
	expect(firstStep(false, account({ talkgroups: [{ id: 1001, priority: true }] }), browser)).toBe('ask');
	expect(firstStep(false, account({ talkgroups: [{ id: 1001 }], look: 'console' }), browser)).toBe('ask');
	expect(firstStep(false, account({ talkgroups: [{ id: 1001 }], look: 'harbour', theme: 'system' }), browser)).toBe('match');
});

test('a browser matched with the account before takes the account’s settings', () => {
	expect(firstStep(true, account({ talkgroups: [{ id: 1002 }] }), browser)).toBe('adopt');
	expect(firstStep(true, null, browser)).toBe('adopt');
});

test('the same profile: talkgroups and marks in any order; nothing else of the browser is in it', () => {
	const a = profileOf([{ id: 1002, priority: true }, { id: 1001 }], 'console', 'dark', true);
	const b = profileOf([{ id: 1001 }, { id: 1002, priority: true }], 'console', 'dark', true);
	expect(sameProfile(a, b)).toBe(true);
	expect(sameProfile(a, { ...b, alerts: false })).toBe(false);
	expect(Object.keys(a).sort()).toEqual(['alerts', 'look', 'talkgroups', 'theme', 'v']);
});
