/// <reference types="node" />
// The PowerShell module against the compose stack (issue #116, ADR 0028,
// SPEC.md A26's module part): every PowerShell "window" is its own pwsh
// process with its own home; the browser it opens is Playwright's, handed
// the URL by a stand-in xdg-open, and signs in through Authentik's own
// pages. Accounts are made up and new for the run.
import { test, expect, type APIRequestContext, type Browser, type Page } from '@playwright/test';
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, readdirSync, statSync, chmodSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const PWSH = process.env.RA_PWSH ?? 'pwsh';
const PS_MODULES = process.env.RA_PS_MODULES ?? '';
const MAILPIT = process.env.RA_MAILPIT_URL ?? 'http://localhost:8025';
const AUTHENTIK = process.env.RA_AUTHENTIK_URL ?? 'http://localhost:9000';
const RADIO = process.env.RA_URL ?? 'http://localhost:8080';
const MODULE = resolve(fileURLToPath(new URL('.', import.meta.url)), '../../powershell/RadioArchive/RadioArchive.psd1');
const root = mkdtempSync(join(tmpdir(), 'ra-module-'));

test.describe.configure({ mode: 'serial' });

// ------------------------------------------------------------ people

const address = (who: string) => `${who}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}@example.test`;
const passwordOf = (email: string) => `correct horse ${email.split('@')[0]} staple`;

async function mails(request: APIRequestContext, to: string) {
	const res = await request.get(`${MAILPIT}/api/v1/search`, { params: { query: `to:"${to}"` } });
	return ((await res.json()) as { messages: { ID: string; Created: string }[] }).messages;
}

async function mail(request: APIRequestContext, to: string, since: Date): Promise<string> {
	const deadline = Date.now() + 60_000;
	while (Date.now() < deadline) {
		const m = (await mails(request, to)).find((m) => new Date(m.Created) > since);
		if (m) return ((await (await request.get(`${MAILPIT}/api/v1/message/${m.ID}`)).json()) as { Text: string }).Text;
		await new Promise((r) => setTimeout(r, 500));
	}
	throw new Error(`no email to ${to} within 60 s`);
}

/** Answers one of Authentik's flow pages. */
async function answer(page: Page, fields: Record<string, string>) {
	const first = page.locator(`input[name="${Object.keys(fields)[0]}"]`);
	await expect(first).toBeVisible({ timeout: 60_000 });
	await expect(page.locator('ak-loading-overlay')).toHaveCount(0, { timeout: 30_000 });
	for (const [name, value] of Object.entries(fields)) await page.locator(`input[name="${name}"]`).fill(value);
	await page.locator('button[type="submit"]').click();
}

async function openMenu(page: Page) {
	const button = page.getByRole('button', { name: /^Account menu/ });
	if ((await button.getAttribute('aria-expanded')) !== 'true') await button.click();
}

async function backInTheRadio(page: Page) {
	await page.waitForURL((u) => u.origin === new URL(RADIO).origin && !u.pathname.startsWith('/auth/'), { timeout: 60_000 });
	await expect(page.getByRole('button', { name: /^Account menu, / })).not.toHaveAccessibleName('Account menu, signed out', { timeout: 30_000 });
}

/** A person: a browser of their own, signed in to the radio. */
type Person = { email: string; page: Page; id: string };

async function account(page: Page) {
	const r = await page.request.get('/api/v1/account');
	return (await r.json()) as { account: { id: string; admin: boolean } | null };
}

/** Signs up (or, if they have before, signs in) from the radio's account menu. */
async function person(browser: Browser, request: APIRequestContext, email: string): Promise<Person> {
	const page = await (await browser.newContext()).newPage();
	await page.goto('/');
	await openMenu(page);
	await page.getByRole('link', { name: 'Sign in' }).click();
	if ((await mails(request, email)).length > 0) {
		await answer(page, { uidField: email });
		await answer(page, { password: passwordOf(email) });
	} else {
		await page.getByRole('link', { name: /Sign up/ }).click();
		const since = new Date(Date.now() - 2000);
		await answer(page, { email, name: 'Made Up Admin', password: passwordOf(email), password_repeat: passwordOf(email) });
		const code = /\b\d{6}\b/.exec(await mail(request, email, since))?.[0];
		expect(code, 'a code in the email').toBeTruthy();
		await answer(page, { code: code! });
	}
	await backInTheRadio(page);
	const id = (await account(page)).account!.id;
	return { email, page, id };
}

async function setRole(owner: Person, who: Person, role: 'admin' | 'listener') {
	const r = await owner.page.request.patch(`/api/v1/admin/accounts/${who.id}`, { data: { role } });
	expect(r.status(), await r.text()).toBe(200);
}

// ------------------------------------------------------------ windows

type Result = { code: number | null; out: string; secrets: string[] };

/** A user's home: their saved sign-ins, and their vault if they have one. */
async function userHome(name: string, vault: boolean): Promise<string> {
	const home = join(root, name);
	mkdirSync(home, { recursive: true });
	if (vault) {
		const r = await window(home, `Register-SecretVault -Name TestStore -ModuleName Microsoft.PowerShell.SecretStore -DefaultVault
Reset-SecretStore -Scope CurrentUser -Authentication None -Interaction None -Force -Confirm:$false 3>$null
(Get-SecretVault -Name TestStore).IsDefault`).done;
		expect(r.out).toContain('True');
	}
	return home;
}

/**
 * One PowerShell window: a pwsh process in home, running body with every
 * stream shown (-Verbose and -Debug on) and a transcript, then $Error.
 * After the transcript ends it reports, for the test alone, the access
 * token this window held and every refresh token in its vault (the
 * secrets), so the test can look for them everywhere else.
 */
function window(home: string, body: string, onOutput?: (out: string) => void) {
	const n = readdirSync(home).filter((f) => f.startsWith('window-')).length + 1;
	const dir = join(home, `window-${n}`);
	mkdirSync(dir);
	const bin = join(dir, 'bin');
	mkdirSync(bin);
	const urlFile = join(dir, 'browser-url');
	writeFileSync(join(bin, 'xdg-open'), `#!/bin/sh\nprintf '%s' "$1" > '${urlFile}'\n`);
	chmodSync(join(bin, 'xdg-open'), 0o755);
	const script = join(dir, 'window.ps1');
	writeFileSync(
		script,
		`$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSStyle.OutputRendering = 'PlainText'
$VerbosePreference = 'Continue'
$DebugPreference = 'Continue'
Import-Module -Name '${MODULE}' -Verbose:$false
$null = Start-Transcript -Path '${join(dir, 'transcript.txt')}'
try {
${body}
} catch {
    'FAILED ' + $_.FullyQualifiedErrorId + ' | ' + $_.Exception.Message
}
'ERRORS:'
$Error | ForEach-Object -Process { $_.FullyQualifiedErrorId + ' | ' + $_.Exception.Message + ' | ' + ($_ | Out-String) }
$null = Stop-Transcript
$VerbosePreference = 'SilentlyContinue'
$DebugPreference = 'SilentlyContinue'
'---SECRETS---'
$c = & (Get-Module -Name RadioArchive) { $script:Connection }
if ($c -and $c.AccessToken) { ConvertFrom-SecureString -SecureString $c.AccessToken -AsPlainText }
if (Get-Module -Name Microsoft.PowerShell.SecretManagement -ListAvailable) {
    foreach ($s in @(Microsoft.PowerShell.SecretManagement\\Get-SecretInfo -ErrorAction SilentlyContinue)) {
        Microsoft.PowerShell.SecretManagement\\Get-Secret -Name $s.Name -AsPlainText
    }
}
`
	);
	const child = spawn(PWSH, ['-NoProfile', '-NonInteractive', '-File', script], {
		env: {
			...process.env,
			HOME: home,
			XDG_DATA_HOME: join(home, '.local/share'),
			XDG_CONFIG_HOME: join(home, '.config'),
			XDG_CACHE_HOME: join(home, '.cache'),
			PATH: `${bin}:${process.env.PATH}`,
			PSModulePath: PS_MODULES,
			POWERSHELL_TELEMETRY_OPTOUT: '1',
			DOTNET_SYSTEM_GLOBALIZATION_INVARIANT: '1'
		}
	});
	let all = '';
	const take = (b: Buffer) => {
		all += b.toString();
		onOutput?.(all);
	};
	child.stdout.on('data', take);
	child.stderr.on('data', take);
	const w = { done: undefined as unknown as Promise<Result>, urlFile, dir, finished: undefined as Result | undefined };
	w.done = new Promise<Result>((ok) =>
		child.on('close', (code) => {
			const [out, secrets = ''] = all.split('---SECRETS---');
			w.finished = { code, out, secrets: secrets.split('\n').map((s) => s.trim()).filter((s) => s.length > 20) };
			ok(w.finished);
		})
	);
	return w;
}

/** Waits for the module to open the browser, and returns the URL. */
async function browserURL(w: ReturnType<typeof window>): Promise<string> {
	await expect
		.poll(() => {
			if (w.finished) throw new Error(`the window ended before opening the browser:\n${w.finished.out}`);
			return existsSync(w.urlFile);
		}, { timeout: 60_000 })
		.toBe(true);
	return readFileSync(w.urlFile, 'utf8');
}

/** The browser half of a sign-in: Authentik's pages, then the module's loopback page. */
async function signInInBrowser(browser: Browser, url: string, email: string) {
	const page = await (await browser.newContext()).newPage();
	await page.goto(url);
	await answer(page, { uidField: email });
	await answer(page, { password: passwordOf(email) });
	await expect(page.getByText("You're signed in to RadioArchive PowerShell. You can close this tab.")).toBeVisible({ timeout: 60_000 });
	expect(new URL(page.url()).hostname).toBe('127.0.0.1');
	await page.context().close();
}

/** Every file under the folders that holds a token as text, UTF-16, hex or Base64. */
function filesHolding(folders: string[], tokens: string[]): string[] {
	const forms = tokens.flatMap((t) => {
		const u8 = Buffer.from(t, 'utf8');
		const u16 = Buffer.from(t, 'utf16le');
		return [u8, u16, Buffer.from(u8.toString('hex')), Buffer.from(u8.toString('hex').toUpperCase()), Buffer.from(u16.toString('hex')),
			Buffer.from(u16.toString('hex').toUpperCase()), Buffer.from(u8.toString('base64')), Buffer.from(u16.toString('base64'))];
	});
	const found: string[] = [];
	const walk = (p: string) => {
		for (const f of readdirSync(p)) {
			const full = join(p, f);
			if (statSync(full).isDirectory()) walk(full);
			else {
				const b = readFileSync(full);
				if (forms.some((form) => b.includes(form))) found.push(full);
			}
		}
	};
	folders.forEach(walk);
	return found;
}

/** Whether text holds a token in any form. */
function holds(text: string, tokens: string[]): boolean {
	return tokens.some((t) => text.includes(t) || text.includes(Buffer.from(t).toString('hex')) || text.includes(Buffer.from(t).toString('base64')));
}

async function refreshRefused(request: APIRequestContext, refresh: string) {
	const r = await request.post(`${AUTHENTIK}/application/o/token/`, {
		form: { grant_type: 'refresh_token', refresh_token: refresh, client_id: 'radioarchive-powershell' }
	});
	expect(r.status()).toBe(400);
	expect(((await r.json()) as { error: string }).error).toBe('invalid_grant');
}

// ------------------------------------------------------------ the tests

let owner: Person;
let admin: Person;
let listener: Person;
let vaultHome: string;
let deviceHome: string;
const connect = `Connect-RadioArchive -Uri '${RADIO}' -AllowUnencryptedAuthentication`;

test.beforeAll(async ({ browser, request }) => {
	test.setTimeout(300_000);
	const initial = process.env.RA_INITIAL_ADMIN_EMAIL;
	expect(initial, 'RA_INITIAL_ADMIN_EMAIL').toBeTruthy();
	// The first admin, as the sign-in tests made them (in another case),
	// or made now if they haven't run.
	owner = await person(browser, request, initial!.toUpperCase());
	expect((await account(owner.page)).account?.admin).toBe(true);
	admin = await person(browser, request, address('module-admin'));
	await setRole(owner, admin, 'admin');
	listener = await person(browser, request, address('module-listener'));
	vaultHome = await userHome('vault', true);
	deviceHome = await userHome('device', true);
});

test('an admin signs in with the browser; a second window uses the saved sign-in; nothing shows a token; Disconnect revokes it', async ({ browser, request }) => {
	const first = window(vaultHome, `${connect} | Format-List -Property Uri, Name, Email, Role, SavedIn
Get-RadioArchiveSystem | Format-List -Property SystemId, Label`);
	await signInInBrowser(browser, await browserURL(first), admin.email);
	const one = await first.done;
	expect(one.out).toMatch(/SavedIn\s*:\s*Vault TestStore/);
	expect(one.out).toMatch(/Email\s*:\s*module-admin-/);
	expect(one.out).toMatch(/Label\s*:\s*EX/);
	expect(one.out).not.toContain('FAILED');
	expect(one.secrets.length, 'the window held an access token and saved a refresh token').toBeGreaterThanOrEqual(2);

	// A second window: the saved sign-in, no browser.
	const second = window(vaultHome, `Get-RadioArchiveTalkgroup -Id 1003 | Format-List -Property Id, State`);
	const two = await second.done;
	expect(existsSync(second.urlFile), 'the second window opened no browser').toBe(false);
	expect(two.out).toMatch(/State\s*:\s*Hidden/);
	expect(two.out).not.toContain('FAILED');

	// No token in any stream, $Error or transcript, nor in any file in
	// plain text, UTF-16, hex or Base64 (the vault's own files hold it
	// encrypted).
	const tokens = [...one.secrets, ...two.secrets];
	expect(holds(one.out, tokens), 'a token in the first window').toBe(false);
	expect(holds(two.out, tokens), 'a token in the second window').toBe(false);
	expect(filesHolding([vaultHome], tokens), 'files holding a token').toEqual([]);
	expect(one.out).toContain('VERBOSE: GET /api/v1/admin/systems');
	expect(one.out).toContain('DEBUG: GET /api/v1/admin/systems answered 200');

	// Disconnect: revoked in Authentik, gone from the vault.
	const saved = two.secrets[two.secrets.length - 1];
	const third = await window(vaultHome, `Disconnect-RadioArchive
@(Microsoft.PowerShell.SecretManagement\\Get-SecretInfo).Count
try { Get-RadioArchiveSystem } catch { 'AFTER ' + $_.FullyQualifiedErrorId }`).done;
	expect(third.out).not.toContain('WARNING');
	expect(third.out).toMatch(/^0$/m);
	expect(third.out).toContain('AFTER not_connected');
	await refreshRefused(request, saved);
});

test('an admin signs in with a device code', async ({ browser }) => {
	let shown = '';
	const w = window(deviceHome, `Connect-RadioArchive -Uri '${RADIO}' -AllowUnencryptedAuthentication -UseDeviceCode | Format-List -Property Role, SavedIn`, (o) => (shown = o));
	await expect.poll(() => /To sign in, open (\S+) on any device and enter the code (\S+)/.test(shown), { timeout: 60_000 }).toBe(true);
	const [, where, code] = /To sign in, open (\S+) on any device and enter the code (\S+)/.exec(shown)!;
	expect(new URL(where).origin).toBe(new URL(AUTHENTIK).origin);
	const page = await (await browser.newContext()).newPage();
	await page.goto(where);
	await answer(page, { uidField: admin.email });
	await answer(page, { password: passwordOf(admin.email) });
	await answer(page, { code });
	const r = await w.done;
	expect(existsSync(w.urlFile), 'no browser was opened on this machine').toBe(false);
	expect(r.out).toMatch(/Role\s*:\s*Admin/);
	expect(r.out).toMatch(/SavedIn\s*:\s*Vault TestStore/);
	expect(holds(r.out, r.secrets)).toBe(false);
	await page.context().close();
});

test("a listener's sign-in is refused and not saved", async ({ browser }) => {
	const home = await userHome('listener', true);
	const w = window(home, `${connect}
'SAVED ' + @(Microsoft.PowerShell.SecretManagement\\Get-SecretInfo).Count`);
	await signInInBrowser(browser, await browserURL(w), listener.email);
	const r = await w.done;
	expect(r.out).toContain('FAILED admin_required');
	expect(r.out).not.toContain('SAVED 1');
	expect(r.secrets, 'no access token kept, nothing in the vault').toEqual([]);
	expect(existsSync(join(home, '.local/share/RadioArchive/SignIns')) && readdirSync(join(home, '.local/share/RadioArchive/SignIns')).length > 0).toBe(false);
});

test('Get -Group North | Set -State Hidden hides 1001; the catalogue drops it at once; the audit log says it was the module (A26)', async ({ request }) => {
	const before = Date.now();
	const r = await window(deviceHome, `Get-RadioArchiveTalkgroup -Group North | Set-RadioArchiveTalkgroup -State Hidden -Confirm:$false
Get-RadioArchiveTalkgroup -Id 1001 | Format-List -Property Id, State
Get-RadioArchiveAuditLog -SystemId 1 -Id 1001 -First 1 | Format-List -Property Via, AccountId, At, Action, Kind`).done;
	try {
		expect(r.out).not.toContain('FAILED');
		expect(r.out).toMatch(/Id\s*:\s*1001\s+State\s*:\s*Hidden/);
		const catalogue = await (await request.get('/api/v1/catalogue')).text();
		expect(catalogue).not.toContain('1001');
		expect(r.out).toMatch(/Via\s*:\s*Module/);
		expect(r.out).toMatch(new RegExp(`AccountId\\s*:\\s*${admin.id}\\b`));
		const at = /At\s*:\s*(.+)/.exec(r.out)![1].trim();
		expect(Math.abs(Date.parse(at) - before)).toBeLessThan(5 * 60_000);
	} finally {
		// 1001 back as it was, for anything after.
		await window(deviceHome, 'Set-RadioArchiveTalkgroup -SystemId 1 -Id 1001 -State Public -Confirm:$false').done;
	}
	expect(await (await request.get('/api/v1/catalogue')).text()).toContain('1001');
});

test('a 1003 recording is saved byte for byte', async () => {
	const expected = process.env.RA_RECORDING_FILE;
	expect(expected, 'RA_RECORDING_FILE (e2e-module.sh)').toBeTruthy();
	const out = join(root, 'saved');
	mkdirSync(out);
	const r = await window(deviceHome, `Get-RadioArchiveCall -SystemId 1 -Talkgroup 1003 -First 1 | Save-RadioArchiveRecording -Path '${out}' | ForEach-Object -Process { 'FILE ' + $_.FullName }`).done;
	expect(r.out).not.toContain('FAILED');
	const file = /FILE (.+\.ogg)/.exec(r.out)![1].trim();
	expect(readFileSync(file).equals(readFileSync(expected!))).toBe(true);
});

test('an admin demoted is refused at their next command', async () => {
	await setRole(owner, admin, 'listener');
	try {
		const r = await window(deviceHome, 'Get-RadioArchiveSystem').done;
		expect(r.out).toContain('FAILED admin_required');
	} finally {
		await setRole(owner, admin, 'admin');
	}
	const again = await window(deviceHome, 'Get-RadioArchiveSystem | Format-List -Property Label').done;
	expect(again.out).toMatch(/Label\s*:\s*EX/);
});

test('with no vault, the sign-in lasts for its window only, with a warning', async ({ browser }) => {
	const home = await userHome('novault', false);
	const w = window(home, `${connect} | Format-List -Property SavedIn`);
	await signInInBrowser(browser, await browserURL(w), admin.email);
	const r = await w.done;
	expect(r.out).toMatch(/WARNING: No SecretManagement vault is registered/);
	expect(r.out).toMatch(/SavedIn\s*:\s*This session only/);
	expect(filesHolding([home], r.secrets)).toEqual([]);
	const next = await window(home, 'Get-RadioArchiveSystem').done;
	expect(next.out).toContain('FAILED not_connected');
});
