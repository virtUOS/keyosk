// The dashboard's behaviour. What it needs from the server, the translated
// words among it, comes from the page's #page-data block.
const DATA = JSON.parse(document.getElementById('page-data').textContent);
const COPIED = DATA.copied;
const BASE_URL = DATA.baseURL;
// What each model is when it is not a chat model, as reported by the
// gateway: "embedding", "rerank", "image" or "vision" (keyprovider.Kind* on
// the server). A model missing here gets the chat example, and every model
// does when the gateway cannot say.
const MODEL_KINDS = DATA.modelKinds || {};

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

// What the examples ask for. None has an ASCII apostrophe: the curl examples
// send their body in single quotes, which one would end.
const RERANK_QUERY = "How do I reset my password?";
const RERANK_DOCUMENTS = [
  "To reset your password, open the account settings.",
  "The library opens at 8 a.m.",
  "Passwords must be at least 12 characters long.",
];
const IMAGE_PROMPT = "A watercolor of Osnabrück’s Rathaus at dusk";
const PAGE_PROMPT = "Transcribe this page as Markdown.";

// The key is referenced as $LLM_API_KEY rather than pasted in: the page only
// ever shows a prefix once the key has been issued, and a command carrying a
// live secret would end up in the user's shell history. It is not called
// OPENAI_API_KEY: the gateway serves models from any vendor, and many people
// already keep a real OpenAI key under that name.
// The example has to match the model. An embedding model, a reranker and an
// image generator each take an endpoint and a body of their own, so the same
// example cannot serve them all: pasting the chat one against bge-m3 just
// fails. A model that reads images takes the chat endpoint, but its example
// sends it a page to read, since that is what such a model is there for.
// Which is which comes from the gateway rather than the name — see
// MODEL_KINDS.
// The chat examples set max_tokens. LiteLLM reserves a request's worst-case
// cost against the budget until the answer is settled, and without max_tokens
// that is the model's whole output limit: on a small budget, the next request
// is refused with "Budget has been exceeded" although little was spent. People
// copy the example as it is, so it should be the request that avoids this.
// The page example allows more than the chat one: a page of text runs longer
// than an answer to "Hello!".
function curlExample(model) {
  switch (MODEL_KINDS[model]) {
    case "embedding":
      return curlPost("/embeddings", model,
        "    \"input\": \"Your text to embed\"\n");
    case "rerank":
      return curlPost("/rerank", model,
        "    \"query\": \"" + RERANK_QUERY + "\",\n" +
        "    \"documents\": [\n" +
        RERANK_DOCUMENTS.map(d => "      \"" + d + "\"").join(",\n") + "\n" +
        "    ],\n" +
        "    \"top_n\": 2\n");
    // The answer carries the image as base64 in JSON, which jq takes out.
    // base64 --decode is spelled the same in GNU and BSD base64.
    case "image":
      return "# Needs jq: https://jqlang.org\n" +
        curlPost("/images/generations", model,
          "    \"prompt\": \"" + IMAGE_PROMPT + "\",\n" +
          "    \"size\": \"1024x1024\",\n" +
          "    \"n\": 1,\n" +
          "    \"response_format\": \"b64_json\"\n") + " \\\n" +
        "  | jq -r '.data[0].b64_json' | base64 --decode > image.png";
    // The page goes in as a data URL. A page image runs to megabytes, more
    // than a command line may hold, so the body is read from stdin rather
    // than passed as an argument, and the shell fills the image into it.
    // tr drops the line breaks GNU base64 adds and BSD base64 does not.
    case "vision":
      return curlHead("/chat/completions") +
        "  -d @- <<EOF\n" +
        "{\n" +
        "  \"model\": \"" + model + "\",\n" +
        "  \"max_tokens\": 2000,\n" +
        "  \"messages\": [{\"role\": \"user\", \"content\": [\n" +
        "    {\"type\": \"image_url\", \"image_url\": {\"url\": \"data:image/png;base64,$(base64 < page.png | tr -d '\\n')\"}},\n" +
        "    {\"type\": \"text\", \"text\": \"" + PAGE_PROMPT + "\"}\n" +
        "  ]}]\n" +
        "}\n" +
        "EOF";
    default:
      return curlPost("/chat/completions", model,
        "    \"max_tokens\": 1000,\n" +
        "    \"messages\": [{\"role\": \"user\", \"content\": \"Hello!\"}]\n");
  }
}

// The start of every curl example: the endpoint, the key and the body's type.
function curlHead(path) {
  return "curl " + BASE_URL + path + " \\\n" +
    "  -H \"Authorization: Bearer $LLM_API_KEY\" \\\n" +
    "  -H \"Content-Type: application/json\" \\\n";
}

// A curl example whose JSON body is the model and then body, given as lines
// that each end in a newline.
function curlPost(path, model, body) {
  return curlHead(path) +
    "  -d '{\n" +
    "    \"model\": \"" + model + "\",\n" +
    body +
    "  }'";
}

// The same request in Python, which is how most people will script against
// the gateway. The key comes from the environment for the same reason as
// above: a secret in a script ends up committed somewhere, and max_tokens is
// there for the same reason as in the curl example.
// Every example goes through the OpenAI SDK but the rerank one: the SDK has
// no call for /rerank, so that is a plain HTTP request with requests.
function pythonExample(model) {
  switch (MODEL_KINDS[model]) {
    case "embedding":
      return pythonClient() +
        "resp = client.embeddings.create(\n" +
        "    model=\"" + model + "\",\n" +
        "    input=\"Your text to embed\",\n" +
        ")\n" +
        "print(resp.data[0].embedding[:5])";
    case "rerank":
      return "# pip install requests\n" +
        "import os\n" +
        "import requests\n\n" +
        "resp = requests.post(\n" +
        "    \"" + BASE_URL + "/rerank\",\n" +
        "    headers={\"Authorization\": \"Bearer \" + os.environ[\"LLM_API_KEY\"]},\n" +
        "    json={\n" +
        "        \"model\": \"" + model + "\",\n" +
        "        \"query\": \"" + RERANK_QUERY + "\",\n" +
        "        \"documents\": [\n" +
        RERANK_DOCUMENTS.map(d => "            \"" + d + "\",\n").join("") +
        "        ],\n" +
        "        \"top_n\": 2,\n" +
        "    },\n" +
        ")\n" +
        "resp.raise_for_status()\n" +
        "for result in resp.json()[\"results\"]:\n" +
        "    print(result[\"index\"], result[\"relevance_score\"])";
    case "image":
      return pythonClient("import base64\n") +
        "resp = client.images.generate(\n" +
        "    model=\"" + model + "\",\n" +
        "    prompt=\"" + IMAGE_PROMPT + "\",\n" +
        "    size=\"1024x1024\",\n" +
        "    n=1,\n" +
        "    response_format=\"b64_json\",\n" +
        ")\n" +
        "with open(\"image.png\", \"wb\") as f:\n" +
        "    f.write(base64.b64decode(resp.data[0].b64_json))";
    case "vision":
      return pythonClient("import base64\n") +
        "with open(\"page.png\", \"rb\") as f:\n" +
        "    page = base64.b64encode(f.read()).decode()\n\n" +
        "resp = client.chat.completions.create(\n" +
        "    model=\"" + model + "\",\n" +
        "    max_tokens=2000,\n" +
        "    messages=[{\"role\": \"user\", \"content\": [\n" +
        "        {\"type\": \"image_url\", \"image_url\": {\"url\": \"data:image/png;base64,\" + page}},\n" +
        "        {\"type\": \"text\", \"text\": \"" + PAGE_PROMPT + "\"},\n" +
        "    ]}],\n" +
        ")\n" +
        "print(resp.choices[0].message.content)";
    default:
      return pythonClient() +
        "resp = client.chat.completions.create(\n" +
        "    model=\"" + model + "\",\n" +
        "    max_tokens=1000,\n" +
        "    messages=[{\"role\": \"user\", \"content\": \"Hello!\"}],\n" +
        ")\n" +
        "print(resp.choices[0].message.content)";
  }
}

// The OpenAI client the Python examples start with, after the imports an
// example needs besides it.
function pythonClient(imports = "") {
  return "# pip install openai\n" +
    imports +
    "import os\n" +
    "from openai import OpenAI\n\n" +
    "client = OpenAI(\n" +
    "    base_url=\"" + BASE_URL + "\",\n" +
    "    api_key=os.environ[\"LLM_API_KEY\"],\n" +
    ")\n\n";
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

// How long until at, in words: "in 5 hours". A moment that has passed is
// "now". Singular forms matter in German: "in 1 Tagen" is wrong. round is
// Math.floor where the text must agree with a count of whole days: rounded,
// 23.6 hours would be "in 1 day".
const inTime = (at, round = Math.round) => {
  const mins = round((at - Date.now()) / 60000);
  const unit = (n, one, many) => `${T_TIME.in} ${n} ${n === 1 ? one : many}`;
  if (mins <= 0) return T_TIME.now;
  if (mins < 60) return unit(mins, T_TIME.minute, T_TIME.minutes);
  const hrs = round(mins / 60);
  if (hrs < 24) return unit(hrs, T_TIME.hour, T_TIME.hours);
  return unit(round(hrs / 24), T_TIME.day, T_TIME.days);
};

// Times in the reader's own zone, and named. The server writes them in UTC,
// which someone elsewhere would have to convert: only the browser knows the
// reader's zone.
const pad = n => String(n).padStart(2, '0');
document.querySelectorAll('time.local-time').forEach(el => {
  const at = new Date(el.dateTime);
  if (isNaN(at)) return;
  const zone = new Intl.DateTimeFormat(document.documentElement.lang, {timeZoneName: 'short'})
    .formatToParts(at).find(p => p.type === 'timeZoneName');
  el.textContent = `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ` +
    `${pad(at.getHours())}:${pad(at.getMinutes())}${zone ? ' ' + zone.value : ''}`;
});

// Colour expiry badges. The server decides when a key is urgent, so the
// badge and the alert above it agree, and both count the whole days left,
// rounded down. With less than a day left the badge says how long.
document.querySelectorAll('.expiry').forEach(el => {
  const at = new Date(el.dataset.expires);
  const left = at - Date.now();
  const days = Math.floor(left / 86400000);
  const cls = left <= 0 ? 'badge-expired' : 'urgent' in el.dataset ? 'badge-warn' : 'badge-ok';
  el.classList.add('badge', cls);
  el.textContent = left <= 0 ? T_TIME.expired : days === 0 ? inTime(at, Math.floor) : days + T_TIME.left;
});

// Quota reset, rendered relative to the reader's clock. A UTC wall time makes
// someone two timezones away do arithmetic to answer "when can I use this
// again". The title carries the exact local moment for anyone who wants it.
document.querySelectorAll('.reset-at').forEach(el => {
  const at = new Date(el.dataset.reset);
  if (isNaN(at)) return;

  // LiteLLM resets lazily, on the first request after the window passes, so
  // a past timestamp means "about to", not "already did".
  const render = () => { el.textContent = inTime(at); };

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
