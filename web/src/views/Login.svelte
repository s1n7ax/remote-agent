<script lang="ts">
  import { onMount } from 'svelte';
  import { app } from '../lib/app.svelte';
  import { errorText } from '../lib/format';
  import { onPairing } from '../lib/pairing';

  let token = $state('');
  let busy = $state(false);
  let error = $state<string | null>(null);

  async function submit(e?: SubmitEvent) {
    e?.preventDefault();
    if (!token.trim()) return;
    busy = true;
    error = null;
    try {
      await app.login(token.trim());
    } catch (err) {
      error = errorText(err);
    } finally {
      busy = false;
    }
  }

  // A pairing link signs in with one tap (see lib/pairing.ts).
  onMount(() =>
    onPairing((t) => {
      token = t;
      void submit();
    }),
  );
</script>

<div class="page">
  <form class="login" onsubmit={submit}>
    <h1>Sign in</h1>
    <p class="muted small">
      Paste the login token from <code>/var/lib/remote-agent/login-token</code> on the server. This browser stays signed in.
    </p>
    <label class="field">
      Login token
      <input type="password" bind:value={token} autocomplete="current-password" autocapitalize="off" spellcheck="false" />
    </label>
    {#if error}<p class="error small">{error}</p>{/if}
    <button class="btn primary" type="submit" disabled={busy || !token.trim()}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>
</div>

<style>
  .login {
    display: grid;
    gap: 12px;
    max-width: 420px;
    margin: 10vh auto 0;
  }
  h1 {
    margin: 0;
  }
  .error {
    color: var(--danger, #b91c1c);
    margin: 0;
  }
</style>
