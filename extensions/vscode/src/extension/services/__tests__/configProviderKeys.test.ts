// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

import { buildCustomCreateSaveEntries } from '@shared/configUtils';
import { applyConfigEntries } from '../configDraft';
import { parseConfig } from '../configParse';

describe('custom provider dictionary keys', () => {
  const names = ['__proto__', 'constructor', 'toString', 'hasOwnProperty'];

  it.each(names)('creates an own entry when selecting the valid name %s', (name) => {
    const draft = applyConfigEntries({}, [{ key: 'provider', value: name }]);

    expect(Object.keys(draft.custom_providers ?? {})).toEqual([name]);
    expect(JSON.parse(JSON.stringify(draft)).custom_providers).toEqual({ [name]: {} });
  });

  it.each(names)('round-trips form fields for %s alongside saved providers', (name) => {
    const base = { custom_providers: { saved: { url: 'https://saved.invalid' } } };
    const entries = buildCustomCreateSaveEntries({
      name, protocol: 'openai', url: 'https://example.invalid/v1', model: 'model',
      models: 'model,other', apiKey: 'fixture-key', authHeader: 'authorization',
    });

    const draft = applyConfigEntries(base, entries);
    const stored = JSON.parse(JSON.stringify(draft));
    const config = parseConfig(JSON.stringify(stored))!;

    expect(Object.keys(config.customProviders).sort()).toEqual([name, 'saved'].sort());
    expect(config.customProviders[name]).toMatchObject({
      protocol: 'openai', url: 'https://example.invalid/v1', model: 'model',
      models: ['model', 'other'], apiKey: 'fixture-key', authHeader: 'authorization',
    });
    expect(stored.custom_providers.saved).toEqual(base.custom_providers.saved);
    expect(base).toEqual({ custom_providers: { saved: { url: 'https://saved.invalid' } } });
  });

  it('retains a __proto__ provider read from CLI JSON as enumerable data', () => {
    const config = parseConfig('{"provider":"__proto__","custom_providers":{"__proto__":{"model":"model","api_key":"fixture-key"}}}')!;

    expect(Object.keys(config.customProviders)).toEqual(['__proto__']);
    expect(JSON.parse(JSON.stringify(config)).customProviders.__proto__).toMatchObject({
      model: 'model', apiKey: 'fixture-key',
    });
  });
});
