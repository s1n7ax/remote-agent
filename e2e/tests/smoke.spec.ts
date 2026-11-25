import { expect, test, type APIRequestContext } from '@playwright/test';

// One ticket end to end: idea → branching question round → design round →
// build opens a PR → CI → verify attaches proof → in review → the user merges.
// GitHub and the agents are fakes (fakegithub, e2e/agents/*.json).

const GH = 'http://127.0.0.1:17800';
const token = 'e2e-login-token'; // written by start.sh
const headers = { 'X-Requested-With': 'remote-agent', Authorization: `Bearer ${token}` };

async function ticket(request: APIRequestContext, id: number) {
  return (await (await request.get(`/api/tickets/${id}`, { headers })).json()).ticket;
}

async function github(request: APIRequestContext) {
  return (await (await request.get(`${GH}/_control/state`)).json())['e2e/demo'];
}

test('the API refuses calls without the login token', async ({ request }) => {
  expect((await request.get('/api/state')).status()).toBe(401);
  const merge = await request.post('/api/projects/1/pulls/1/merge', { headers: { 'X-Requested-With': 'remote-agent' } });
  expect(merge.status()).toBe(401);
  expect((await request.get('/healthz')).status()).toBe(200);
});

test('a ticket goes from idea to merged', async ({ page, request }) => {
  // Sign in: first the token screen, then a wrong and the right token.
  await page.goto('/');
  await page.getByLabel('Login token').fill('wrong');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByText('wrong login token')).toBeVisible();
  await page.goto(`/#/login?token=${token}`);
  await expect(page.getByRole('dialog', { name: 'Projects' })).toBeVisible();
  expect(page.url()).not.toContain(token); // the pairing token leaves the address bar

  // Add a project through the UI, then the demo project (fake agent) via the API.
  // With no projects the Projects sheet opens on its own.
  const sheet = page.getByRole('dialog', { name: 'Projects' });
  await sheet.getByRole('button', { name: 'Add project' }).click();
  await sheet.getByPlaceholder('owner/name').fill('e2e/other');
  await sheet.getByRole('button', { name: 'Add project' }).click();
  await expect(page.getByRole('banner').getByRole('button', { name: /other/ })).toBeVisible();

  const res = await request.post('/api/projects', { headers, data: { repo: 'e2e/demo', agent: 'fake' } });
  expect(res.ok()).toBeTruthy();
  const project = await res.json();

  await page.goto(`/#/p/${project.id}/board`);
  await page.getByRole('button', { name: 'New ticket' }).click();
  await page.getByPlaceholder(/What should change/).fill('Add a greeting to the README');
  await page.getByRole('button', { name: 'Create & start' }).click();
  await expect(page).toHaveURL(/#\/p\/\d+\/t\/\d+\/log/);
  const id = Number(/\/t\/(\d+)\//.exec(page.url())![1]);
  expect((await ticket(request, id)).issue).toBeGreaterThan(0);

  // Round 1: picking "A new file" reveals a follow-up question.
  await page.getByRole('link', { name: /Questions/ }).click();
  await expect(page.getByText('Where should the greeting go?')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText('Which file name?')).toHaveCount(0);
  await page.getByText('A new file').click();
  await expect(page.getByText('Which file name?')).toBeVisible();
  await page.getByText('Casual').click();
  expect((await ticket(request, id)).status).toBe('needs_input');
  await page.getByRole('textbox', { name: 'Which file name?' }).fill('HELLO.md');
  await page.getByRole('textbox', { name: 'Which file name?' }).blur();

  // Round 2 (design) arrives once round 1 is answered.
  await expect(page.getByText('Build it as described?')).toBeVisible({ timeout: 30_000 });
  await page.getByRole('button', { name: 'Yes, that’s right' }).click();

  // The app builds, opens a PR, waits for CI, verifies and marks it ready.
  await expect.poll(async () => (await ticket(request, id)).status, { timeout: 60_000 }).toBe('in_review');
  const t = await ticket(request, id);
  expect(t.title).toBe('Add a greeting');
  expect(t.verifiedSha).toBeTruthy();
  const gh = await github(request);
  expect(gh.pulls[t.pr].draft).toBe(false);
  expect(gh.merges ?? []).toHaveLength(0); // agents never merge

  await page.goto(`/#/p/${project.id}/t/${id}/proof`);
  await expect(page.getByText('Greeting present')).toBeVisible();
  await expect(page.getByRole('img').first()).toHaveJSProperty('complete', true);
  expect(await page.getByRole('img').first().evaluate((i: HTMLImageElement) => i.naturalWidth)).toBeGreaterThan(0);
  await page.screenshot({ path: 'test-results/proof.png', fullPage: true });

  await page.goto(`/#/p/${project.id}/t/${id}/pr`);
  await page.getByRole('button', { name: 'Merge…' }).click();
  await page.getByRole('button', { name: 'Merge pull request' }).click();
  await expect.poll(async () => (await github(request)).merges?.length ?? 0).toBe(1);
  await expect.poll(async () => (await ticket(request, id)).status).toBe('done');
});

test('one tap removes a skill and Undo brings it back', async ({ page, request }) => {
  await page.goto(`/#/login?token=${token}`);
  await page.goto('/#/skills');
  const remove = page.getByRole('button', { name: 'Remove todo-comments' });
  await expect(remove).toBeVisible();
  await remove.click();
  await expect(page.getByRole('status').filter({ hasText: 'Removed todo-comments' })).toBeVisible();
  await expect(remove).toHaveCount(0);
  const names = async () => ((await (await request.get('/api/skills', { headers })).json()) as { name: string }[]).map((s) => s.name);
  expect(await names()).not.toContain('todo-comments');
  await page.getByRole('button', { name: 'Undo' }).click();
  await expect(remove).toBeVisible();
  expect(await names()).toContain('todo-comments');
});

// Runs after the tests above, which add the e2e/demo project.
test('images go on a ticket and stay on the board', async ({ page, request }) => {
  const projects = (await (await request.get('/api/projects', { headers })).json()) as { id: number; repo: string }[];
  const project = projects.find((p) => p.repo === 'e2e/demo')!;
  const png = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
    'base64',
  );
  const file = (name: string) => ({ name, mimeType: 'image/png', buffer: png });

  await page.goto(`/#/login?token=${token}`);
  await page.goto(`/#/p/${project.id}/board`);
  await page.getByRole('button', { name: 'New ticket' }).click();
  const sheet = page.getByRole('dialog', { name: 'New ticket' });
  await sheet.getByPlaceholder(/What should change/).fill('The logo is blurry, see image 2');
  await sheet.locator('input[type=file]').setInputFiles([file('a.png'), file('b.png')]);
  await expect(sheet.getByRole('list', { name: 'Images' }).getByRole('listitem')).toHaveCount(2);
  await sheet.locator('input[type=file]').setInputFiles({ name: 'doc.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.7') });
  await expect(sheet.getByText('Only images (JPEG, PNG, WebP, GIF) can be attached.')).toBeVisible();
  await expect(sheet.getByText('Uploading…')).toHaveCount(0);
  await sheet.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(sheet).toBeHidden();

  const board = (await (await request.get(`/api/projects/${project.id}/board`, { headers })).json()).columns.backlog as { id: number; title: string }[];
  const id = board.find((t) => t.title.startsWith('The logo is blurry'))!.id;
  await page.goto(`/#/p/${project.id}/t/${id}/requirement`);
  const panel = page.getByRole('region', { name: 'Images' });
  await expect(panel.getByRole('img')).toHaveCount(2);
  await panel.locator('input[type=file]').setInputFiles(file('c.png'));
  await expect(panel.getByRole('img', { name: 'Image 3' })).toBeVisible();
  page.once('dialog', (d) => d.accept());
  await panel.getByRole('button', { name: 'Remove image 2' }).click();
  await expect(panel.getByRole('img')).toHaveCount(2);
  await expect(panel.getByRole('img', { name: 'Image 3' })).toBeVisible();

  // Over the API the server enforces the type itself.
  const pdf = await request.post(`/api/tickets/${id}/images`, {
    headers,
    multipart: { file: { name: 'x.png', mimeType: 'image/png', buffer: Buffer.from('%PDF-1.7 not a png') } },
  });
  expect(pdf.status()).toBe(400);
  expect((await pdf.json()).error).toContain('only images');

  // The issue only counts the images.
  const issueNo = (await ticket(request, id)).issue;
  const body: string = (await github(request)).issues[issueNo].body;
  expect(body).toContain('_2 images attached on the board._');
  expect(body).not.toContain('/api/artifacts');
});
