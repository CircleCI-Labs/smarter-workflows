'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { test } = require('node:test');

const { buildGreeting, renderGreeting } = require('../src/greeting.js');

const schema = JSON.parse(
  fs.readFileSync(path.join(__dirname, '..', '..', '..', 'libs', 'proto', 'schema.json'), 'utf8'),
);

test('a greeting follows the shared schema', () => {
  const greeting = buildGreeting('Ada');
  for (const key of schema.required) {
    assert.ok(key in greeting, `missing the required property ${key}`);
  }
  for (const [key, prop] of Object.entries(schema.properties)) {
    if (key in greeting && prop.type === 'string') {
      assert.equal(typeof greeting[key], 'string', `${key} must be a string`);
    }
  }
  assert.equal(greeting.message, 'Hello, Ada!');
});

test('a blank name greets the world', () => {
  assert.deepEqual(buildGreeting('   '), { message: 'Hello, world!', name: 'world' });
  assert.deepEqual(buildGreeting(undefined), { message: 'Hello, world!', name: 'world' });
});

test('the rendered greeting is escaped', () => {
  assert.equal(
    renderGreeting({ message: '<b>"hi"</b>' }),
    '<p class="greeting">&lt;b&gt;&quot;hi&quot;&lt;/b&gt;</p>',
  );
});
