// The dashboard's behaviour. What it needs from the server, the translated
// words among it, comes from the page's #page-data block.
const DATA = JSON.parse(document.getElementById('page-data').textContent);
const COPIED = DATA.copied;
const BASE_URL = DATA.baseURL;
// Which models take /embeddings instead of /chat/completions, as reported by
// the gateway. Empty when it cannot say, in which case every model gets the
// chat example — the behaviour before this was threaded through.
const EMBEDDING_MODELS = DATA.embeddingModels || {};

// The token in ?k= that brought a new key here is spent once the page has
// rendered. Drop it from the address bar so a reload or a copied link does not
// carry it.
if (new URLSearchParams(location.search).has('k')) {
  const u = new URL(location.href);
  u.searchParams.delete('k');
  history.replaceState(null, '', u.pathname + u.search + u.hash);
}

// Time words, translated server-side: the browser does the arithmetic, but
// the wording has to match the page.
const T_TIME = DATA.time;

// Send each key form once. A second click while the first request is still
// running would only queue another key change; the server serializes them, but
// the user gains nothing from it. Runs on submit, after the confirm() in
// confirm.js: a declined confirm leaves the button usable, and disabling on
// click instead can cancel the submit in some browsers.
document.querySelectorAll('form[data-once]').forEach(form => {
  form.addEventListener('submit', e => {
    if (e.defaultPrevented) return;
    if (form.dataset.sent) { e.preventDefault(); return; }
    form.dataset.sent = '1';
    form.querySelectorAll('button[type=submit]').forEach(b => b.disabled = true);
  });
});
// Back from the next page can restore this one from the bfcache with its
// buttons still disabled.
window.addEventListener('pageshow', e => {
  if (!e.persisted) return;
  document.querySelectorAll('form[data-once]').forEach(form => {
    delete form.dataset.sent;
    form.querySelectorAll('button[type=submit]').forEach(b => b.disabled = false);
  });
});

// A copy button names the element whose text it copies in data-copy.
document.querySelectorAll('button[data-copy]').forEach(btn => {
  btn.addEventListener('click', () => {
    const text = document.getElementById(btn.dataset.copy).textContent.trim();
    navigator.clipboard.writeText(text).then(() => {
      const orig = btn.textContent;
      btn.textContent = COPIED;
      setTimeout(() => btn.textContent = orig, 2000);
    });
  });
});

// Which example the panel shows: curl or Python. Remembered per browser, as a
// convenience only — storage can be blocked, and the default is fine then.
const EXAMPLE_LANG_KEY = 'exampleLang';
let exampleLang = 'curl';
let exampleModel = '';
try {
  if (localStorage.getItem(EXAMPLE_LANG_KEY) === 'python') exampleLang = 'python';
} catch (e) {}

// Shows the request around a model, for the people who want a whole example
// rather than the name. Focus is not moved: the click was a copy, and yanking
// the caret down the page would fight anyone tabbing through the chips.
function showExample(model) {
  const panel = document.getElementById('curl-panel');
  if (!panel) return;
  exampleModel = model;
  document.getElementById('curl-model').textContent = model;
  document.getElementById('curl-code').textContent =
    exampleLang === 'python' ? pythonExample(model) : curlExample(model);
  panel.querySelectorAll('.example-tabs button').forEach(b =>
    b.setAttribute('aria-pressed', String(b.dataset.lang === exampleLang)));
}

function setExampleLang(lang) {
  exampleLang = lang;
  try { localStorage.setItem(EXAMPLE_LANG_KEY, lang); } catch (e) {}
  showExample(exampleModel);
}
document.querySelectorAll('.example-tabs button').forEach(b =>
  b.addEventListener('click', () => setExampleLang(b.dataset.lang)));

// Copies the example currently on show. Separate from the chips so that
// copying the name and copying the request are never the same gesture.
function copyCurl(btn) {
  const code = document.getElementById('curl-code');
  if (!code) return;
  navigator.clipboard.writeText(code.textContent).then(() => {
    if (btn.dataset.busy) return;
    btn.dataset.busy = '1';
    const label = btn.textContent;
    btn.textContent = COPIED;
    setTimeout(() => {
      btn.textContent = label;
      delete btn.dataset.busy;
    }, 1200);
  });
}
{
  const btn = document.getElementById('curl-copy');
  if (btn) btn.addEventListener('click', () => copyCurl(btn));
}

// The key is referenced as $LLM_API_KEY rather than pasted in: the page only
// ever shows a prefix once the key has been issued, and a command carrying a
// live secret would end up in the user's shell history. It is not called
// OPENAI_API_KEY: the gateway serves models from any vendor, and many people
// already keep a real OpenAI key under that name.
// An embedding model takes a different endpoint and a different body than a
// chat model, so the same example cannot serve both: pasting the chat one
// against bge-m3 just fails. Which is which comes from the gateway rather than
// the name — see EMBEDDING_MODELS.
// The chat example sets max_tokens. LiteLLM reserves a request's worst-case
// cost against the budget until the answer is settled, and without max_tokens
// that is the model's whole output limit: on a small budget, the next request
// is refused with "Budget has been exceeded" although little was spent. People
// copy the example as it is, so it should be the request that avoids this.
function curlExample(model) {
  const embedding = EMBEDDING_MODELS[model] === true;
  const path = embedding ? "/embeddings" : "/chat/completions";
  const body = embedding
    ? "    \"input\": \"Your text to embed\"\n"
    : "    \"max_tokens\": 1000,\n" +
      "    \"messages\": [{\"role\": \"user\", \"content\": \"Hello!\"}]\n";
  return "curl " + BASE_URL + path + " \\\n" +
    "  -H \"Authorization: Bearer $LLM_API_KEY\" \\\n" +
    "  -H \"Content-Type: application/json\" \\\n" +
    "  -d '{\n" +
    "    \"model\": \"" + model + "\",\n" +
    body +
    "  }'";
}

// The same request through the OpenAI Python SDK, which is how most people
// will script against the gateway. The key comes from the environment for the
// same reason as above: a secret in a script ends up committed somewhere, and
// max_tokens is there for the same reason as in the curl example.
function pythonExample(model) {
  const embedding = EMBEDDING_MODELS[model] === true;
  const call = embedding
    ? "resp = client.embeddings.create(\n" +
      "    model=\"" + model + "\",\n" +
      "    input=\"Your text to embed\",\n" +
      ")\n" +
      "print(resp.data[0].embedding[:5])"
    : "resp = client.chat.completions.create(\n" +
      "    model=\"" + model + "\",\n" +
      "    max_tokens=1000,\n" +
      "    messages=[{\"role\": \"user\", \"content\": \"Hello!\"}],\n" +
      ")\n" +
      "print(resp.choices[0].message.content)";
  return "# pip install openai\n" +
    "import os\n" +
    "from openai import OpenAI\n\n" +
    "client = OpenAI(\n" +
    "    base_url=\"" + BASE_URL + "\",\n" +
    "    api_key=os.environ[\"LLM_API_KEY\"],\n" +
    ")\n\n" +
    call;
}

// Clicking a model copies its bare name: that is what goes into an SDK call, a
// config file or an environment variable, and it is the thing that is tedious
// to retype correctly. The example below switches to it rather than going on
// the clipboard, so wanting one does not cost the other.
//
// The label is swapped rather than the code element replaced, so the button
// keeps its width and the row does not jump.
function copyModel(btn) {
  showExample(btn.dataset.model);
  navigator.clipboard.writeText(btn.dataset.model).then(() => {
    if (btn.dataset.busy) return;
    btn.dataset.busy = '1';

    // Say so in words, the same as the other copy buttons: a colour change
    // alone reads as a hover effect rather than confirmation. The width is
    // pinned first so the row does not reflow while the label is swapped.
    const code = btn.querySelector('code');
    code.style.minWidth = code.offsetWidth + 'px';
    const name = btn.dataset.model;
    code.textContent = COPIED;
    btn.classList.add('copied');

    setTimeout(() => {
      code.textContent = name;
      code.style.minWidth = '';
      btn.classList.remove('copied');
      delete btn.dataset.busy;
    }, 1200);
  });
}
document.querySelectorAll('.model-copy').forEach(btn =>
  btn.addEventListener('click', () => copyModel(btn)));

// Fill the example for the first model, so the panel is never empty.
{
  const first = document.querySelector('.model-copy');
  if (first) showExample(first.dataset.model);
}

// Colour expiry badges. The server decides when a key is urgent, so the
// badge and the alert above it agree.
document.querySelectorAll('.expiry').forEach(el => {
  const d = new Date(el.dataset.date + 'T23:59:59');
  const days = Math.round((d - Date.now()) / 86400000);
  const cls = days < 0 ? 'badge-expired' : 'urgent' in el.dataset ? 'badge-warn' : 'badge-ok';
  el.classList.add('badge', cls);
  el.textContent = days < 0 ? T_TIME.expired : days === 0 ? T_TIME.today : days + T_TIME.left;
});

// Quota reset, rendered relative to the reader's clock. A UTC wall time makes
// someone two timezones away do arithmetic to answer "when can I use this
// again". The title carries the exact local moment for anyone who wants it.
document.querySelectorAll('.reset-at').forEach(el => {
  const at = new Date(el.dataset.reset);
  if (isNaN(at)) return;

  const render = () => {
    const mins = Math.round((at - Date.now()) / 60000);
    // LiteLLM resets lazily, on the first request after the window passes, so
    // a past timestamp means "about to", not "already did".
    // Singular forms matter in German: "in 1 Tagen" is wrong.
    const unit = (n, one, many) => `${T_TIME.in} ${n} ${n === 1 ? one : many}`;
    if (mins <= 0) { el.textContent = T_TIME.now; return; }
    if (mins < 60) { el.textContent = unit(mins, T_TIME.minute, T_TIME.minutes); return; }
    const hrs = Math.round(mins / 60);
    if (hrs < 24) { el.textContent = unit(hrs, T_TIME.hour, T_TIME.hours); return; }
    el.textContent = unit(Math.round(hrs / 24), T_TIME.day, T_TIME.days);
  };

  render();
  el.title = at.toLocaleString();
  setInterval(render, 30000);
});

// Session polling — redirect to /login when session expires
(function poll() {
  if (document.hidden) { setTimeout(poll, 5000); return; }
  fetch('/session/status').then(r => {
    if (r.status === 401) { location.href = '/login'; return; }
    setTimeout(poll, 5000);
  }).catch(() => setTimeout(poll, 10000));
})();
