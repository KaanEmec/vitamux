// Pinning to the dashboard from Explore: a card in the stored layout (GET/PUT /settings/dashboard).
// Pinning adds a card at the end (or shows a hidden one); unpinning removes it.
import { api, type Problem, type Schemas } from '../api/client.ts';

export class Pins {
	layout = $state<Schemas['DashboardLayout'] | null>(null);
	problem = $state<Problem | null>(null);

	async load() {
		const res = await api.GET('/api/v1/settings/dashboard');
		this.layout = res.data ?? null;
	}

	has(key: string): boolean {
		return !!this.layout?.cards.some((c) => c.metric === key && !c.hidden);
	}

	async toggle(key: string) {
		if (!this.layout) return;
		const cards = this.layout.cards;
		const next = this.has(key)
			? cards.filter((c) => c.metric !== key)
			: cards.some((c) => c.metric === key)
				? cards.map((c) => (c.metric === key ? { ...c, hidden: false } : c))
				: [...cards, { metric: key, size: 'M' as const, hidden: false }];
		const res = await api.PUT('/api/v1/settings/dashboard', { body: { version: 1, cards: next } });
		this.problem = res.error ?? null;
		if (res.data) this.layout = res.data;
	}
}
