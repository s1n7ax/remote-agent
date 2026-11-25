import { describe, expect, it } from 'vitest';
import { blindWarning, isHeic, MAX_IMAGES, NOT_IMAGE, refusal, seesImages, TOO_BIG, TOO_MANY } from './images';
import type { AppState } from './types';

const file = (name: string, type: string, size = 10) => new File([new Uint8Array(size)], name, { type });

describe('image checks', () => {
  it('accepts the usual formats and refuses other files', () => {
    for (const [n, t] of [
      ['a.jpg', 'image/jpeg'],
      ['a.png', 'image/png'],
      ['a.webp', 'image/webp'],
      ['a.gif', 'image/gif'],
      ['unknown', ''],
    ])
      expect(refusal(file(n, t), 0)).toBeNull();
    expect(refusal(file('a.pdf', 'application/pdf'), 0)).toBe(NOT_IMAGE);
    expect(refusal(file('a.txt', 'text/plain'), 0)).toBe(NOT_IMAGE);
    expect(refusal(file('a.svg', 'image/svg+xml'), 0)).toBe(NOT_IMAGE);
  });

  it('names the limits', () => {
    expect(refusal(file('a.png', 'image/png'), MAX_IMAGES)).toBe(TOO_MANY);
    expect(TOO_MANY).toContain('10');
    expect(refusal(file('a.png', 'image/png', (20 << 20) + 1), 0)).toBe(TOO_BIG);
    expect(TOO_BIG).toContain('20 MB');
  });

  it('spots iPhone photos by type or name', () => {
    expect(isHeic(file('IMG_1.HEIC', ''))).toBe(true);
    expect(isHeic(file('x', 'image/heif'))).toBe(true);
    expect(isHeic(file('x.jpg', 'image/jpeg'))).toBe(false);
  });

  it('knows which models see images', () => {
    const st = { imageModels: { pi: ['anthropic/claude-sonnet-latest'] } } as unknown as AppState;
    expect(seesImages(st, 'claude', 'haiku')).toBe(true);
    expect(seesImages(st, 'pi', 'anthropic/claude-sonnet-latest')).toBe(true);
    expect(seesImages(st, 'pi', 'google/gemini-3-pro')).toBe(false);
    expect(seesImages({ imageModels: { pi: null } } as unknown as AppState, 'pi', 'x')).toBe(false);
    expect(blindWarning(1)).toContain('your 1 image but');
    expect(blindWarning(3)).toContain('your 3 images');
  });
});
