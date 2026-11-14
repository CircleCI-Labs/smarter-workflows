'use strict';

// buildGreeting makes the same greeting as the api: the shape of
// libs/proto/schema.json.
function buildGreeting(name) {
  const who = typeof name === 'string' && name.trim() !== '' ? name.trim() : 'world';
  return { message: `Hello, ${who}!`, name: who };
}

const escapes = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };

// renderGreeting is the HTML of a greeting. The text is escaped.
function renderGreeting(greeting) {
  const text = String(greeting.message).replace(/[&<>"']/g, (c) => escapes[c]);
  return `<p class="greeting">${text}</p>`;
}

module.exports = { buildGreeting, renderGreeting };
