import adapter from '@sveltejs/adapter-static';
import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig } from 'vite';

export default defineConfig({
	plugins: [
		sveltekit({
			// A single-page app, embedded in the API binary and served by it
			// for every path outside the API (ADR 0001, web/web.go).
			adapter: adapter({ fallback: 'index.html', strict: true })
		})
	],
	server: {
		// `npm run dev` talks to a local API.
		proxy: { '/api': 'http://127.0.0.1:8080', '/auth': 'http://127.0.0.1:8080' }
	}
});
