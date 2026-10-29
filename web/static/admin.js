// The admin panel's behaviour. What it needs from the server, the translated
// words among it, comes from the page's #page-data block.
const DATA = JSON.parse(document.getElementById('page-data').textContent);

// The deadline's follow-up controls are meaningless without a date, so they
// appear with one. Server-rendered state decides the initial visibility; this
// only keeps it in step as the admin types.
function toggleExpiryOptions(input) {
  const opts = input.closest('form').querySelector('.expiry-options');
  if (opts) opts.hidden = !input.value;
}
document.querySelectorAll('input[name="expires_at"]').forEach(input =>
  input.addEventListener('input', () => toggleExpiryOptions(input)));

// The promotion form's dropdown, narrowed by the filter box. Options are
// swapped in and out rather than hidden, since not every browser hides a
// hidden <option> inside a native <select>.
const adminCandidates = Array.from(
  document.querySelectorAll('#admin-candidates option')).filter(o => o.value !== '');
function filterCandidates(q) {
  q = q.trim().toLowerCase();
  const sel = document.getElementById('admin-candidates');
  sel.replaceChildren(sel.options[0], ...adminCandidates.filter(
    o => q === '' || o.textContent.toLowerCase().includes(q)));
}
{
  const filter = document.getElementById('admin-filter');
  if (filter) filter.addEventListener('input', () => filterCandidates(filter.value));
}

// The filter boxes above the users and the audit log hide the rows that do
// not contain every word typed, in any order, so "alice revoked" finds what
// happened to Alice without knowing which column comes first. Each row's text
// is gathered once, from its cells without the row menu, whose entries
// ("Edit", "Revoke key") would otherwise match every row. Not from innerText:
// a hidden row's innerText falls back to the raw text, menu included.
function rowText(tr) {
  return Array.from(tr.cells).filter(td => !td.classList.contains('cell-menu'))
    .map(td => td.textContent).join(' ').replace(/\s+/g, ' ').toLowerCase();
}
document.querySelectorAll('.table-filter').forEach(input => {
  const tbody = document.getElementById(input.dataset.filter).tBodies[0];
  const noMatch = tbody.querySelector('.no-match');
  const rows = Array.from(tbody.rows).filter(r => !r.classList.contains('empty-row'))
    .map(r => [r, rowText(r)]);
  const apply = () => {
    const words = input.value.toLowerCase().split(/\s+/).filter(Boolean);
    let shown = 0;
    for (const [r, text] of rows) {
      r.hidden = !words.every(w => text.includes(w));
      if (!r.hidden) shown++;
    }
    noMatch.hidden = shown > 0 || rows.length === 0;
  };
  input.addEventListener('input', apply);
  // A browser may bring the box back filled, on a reload or on the way back
  // from another page; the rows then agree with it from the start.
  apply();
});

// The selected tab lives in the URL hash, so a reload or a shared link comes
// back to it. "admin" is accepted as an alias for "admins".
function showTab(name) {
  if (name === 'admin') name = 'admins';
  if (!document.getElementById('tab-' + name)) name = 'profiles';
  document.querySelectorAll('.tab-panel').forEach(p => p.classList.toggle('active', p.id === 'tab-' + name));
  document.querySelectorAll('.tab-btn').forEach(b => b.classList.toggle('active', b.dataset.tab === name));
}

document.querySelector('.tabs').addEventListener('click', e => {
  const btn = e.target.closest('.tab-btn');
  if (!btn) return;
  showTab(btn.dataset.tab);
  // replaceState: no scroll jump, no history entry per click. The query is
  // dropped so a reload does not show a stale flash message again.
  history.replaceState(null, '', location.pathname + '#' + btn.dataset.tab);
});

window.addEventListener('hashchange', () => showTab(location.hash.slice(1)));

// The users' edit dialogs open from their row menu entry, which names the
// dialog in data-dialog; every dialog's Cancel button closes it.
document.addEventListener('click', e => {
  const open = e.target.closest('button[data-dialog]');
  if (open) document.getElementById(open.dataset.dialog).showModal();
  const close = e.target.closest('button[data-close-dialog]');
  if (close) close.closest('dialog').close();
});

const profileDialog = document.getElementById('profile-dialog');
const PROFILE_TEXT = DATA.profile;

function openNewProfile() {
  resetProfileForm();
  profileDialog.showModal();
  document.getElementById('pf-name').focus();
}
document.getElementById('profile-new').addEventListener('click', openNewProfile);

// The edit entry carries the profile's current values in data attributes.
function openEditProfile(btn) {
  const p = btn.dataset;
  document.getElementById('pf-id').value = p.id;
  document.getElementById('pf-name').value = p.name;
  document.getElementById('pf-desc').value = p.desc;
  setSelectedModels(p.models);
  document.getElementById('pf-tpm').value = p.tpm;
  document.getElementById('pf-rpm').value = p.rpm;
  document.getElementById('pf-expiry').value = p.expiry;
  setQuotaRows(JSON.parse(p.quotas));
  setDefaultLocked('default' in p);
  document.getElementById('profile-form-title').textContent = PROFILE_TEXT.editTitle + ' ' + p.name;
  document.getElementById('profile-submit-btn').textContent = PROFILE_TEXT.update;
  document.getElementById('profile-form').action = '/admin/profiles/' + p.id;
  profileDialog.showModal();
  document.getElementById('pf-name').focus();
}
document.querySelectorAll('.profile-edit').forEach(btn =>
  btn.addEventListener('click', () => openEditProfile(btn)));

// Quota windows are a repeating group: a profile can hold several allowances
// at once ("0.10/day AND 5/month"), which the gateway enforces
// independently. Rows post as parallel quota_budget/quota_period arrays and
// are paired by position on the server.
const QUOTA_TEXT = DATA.quota;
// Each period carries its length in hours, the same as periodHours on the
// server, so the length comparison below does not depend on the menu order.
const QUOTA_PERIODS = DATA.periods;

function quotaRow(budget, period) {
  const row = document.createElement('div');
  row.className = 'quota-row';

  const n = document.createElement('input');
  n.type = 'number';
  n.name = 'quota_budget';
  n.min = '0';
  n.step = 'any';
  n.placeholder = QUOTA_TEXT.amount;
  n.value = budget || '';
  n.addEventListener('input', checkQuotaRows);

  const sel = document.createElement('select');
  sel.name = 'quota_period';
  for (const [value, label] of QUOTA_PERIODS) {
    const o = document.createElement('option');
    o.value = value;
    o.textContent = label;
    if (value === period) o.selected = true;
    sel.appendChild(o);
  }
  sel.addEventListener('change', checkQuotaRows);

  const del = document.createElement('button');
  del.type = 'button';
  del.className = 'btn btn-danger-quiet';
  del.textContent = '×';
  del.setAttribute('aria-label', QUOTA_TEXT.delete);
  del.onclick = () => { row.remove(); checkQuotaRows(); };

  row.append(n, sel, del);
  return row;
}

// A new row starts at the first period no other row uses, so adding a
// second window does not silently repeat the first one's period. Once every
// period is taken there is nothing left to add, and the button hides.
function addQuotaRow(budget, period) {
  if (!period) {
    const used = new Set(quotaRows().map(r => r.period.value));
    period = (QUOTA_PERIODS.find(([value]) => !used.has(value)) || QUOTA_PERIODS[0])[0];
  }
  document.getElementById('pf-quotas').appendChild(quotaRow(budget, period));
  checkQuotaRows();
}
document.getElementById('pf-quota-add').addEventListener('click', () => addQuotaRow());

function setQuotaRows(quotas) {
  const box = document.getElementById('pf-quotas');
  box.textContent = '';
  for (const q of (quotas || [])) addQuotaRow(q.Budget, q.Period);
  checkQuotaRows();
}

function quotaRows() {
  return [...document.querySelectorAll('#pf-quotas .quota-row')].map(row => ({
    amount: row.querySelector('input[name="quota_budget"]'),
    period: row.querySelector('select[name="quota_period"]'),
  }));
}

// checkQuotaRows marks the windows the server would refuse, so the browser
// holds the form back and points at the mistake instead of the admin losing
// every change to a failed save. It checks what parseQuotaWindows and
// checkWindowsBind in internal/handlers/admin.go check; keep the two in step.
// A row without a positive amount is dropped on save, so it is left out.
function checkQuotaRows() {
  const rows = quotaRows();
  document.getElementById('pf-quota-add').hidden = rows.length >= QUOTA_PERIODS.length;
  const entry = p => QUOTA_PERIODS.find(([value]) => value === p);
  const hours = p => entry(p)[2];
  const label = p => entry(p)[1];
  const active = rows.filter(r => parseFloat(r.amount.value.replace(',', '.')) > 0);
  // A period's first row is the window; later rows on it are duplicates, and
  // only the window takes part in the length comparison, as on the server.
  const windows = active.filter((r, i) => !active.slice(0, i).some(o => o.period.value === r.period.value));
  for (const r of rows) {
    r.amount.setCustomValidity('');
    r.period.setCustomValidity('');
  }
  active.forEach(r => {
    if (!windows.includes(r)) {
      r.period.setCustomValidity(QUOTA_TEXT.duplicate);
      return;
    }
    const amount = parseFloat(r.amount.value.replace(',', '.'));
    const longer = windows.find(o => hours(o.period.value) > hours(r.period.value) &&
      parseFloat(o.amount.value.replace(',', '.')) < amount);
    if (longer) {
      r.amount.setCustomValidity(QUOTA_TEXT.exceeds.replace('{period}', label(longer.period.value)));
    }
  });
  // The browser only shows a field's message on submit; this keeps the first
  // one in view while the admin is still editing.
  const error = document.getElementById('pf-quota-error');
  const first = rows.flatMap(r => [r.amount, r.period]).find(f => f.validationMessage);
  error.textContent = first ? first.validationMessage : '';
  error.hidden = !first;
}

// Models arrive as a comma-separated string; tick the matching boxes, or fall
// back to the free-text input when the gateway list was unavailable.
function setSelectedModels(models) {
  const wanted = new Set((models || '').split(',').map(s => s.trim()).filter(Boolean));
  const picker = document.getElementById('pf-model-picker');
  if (!picker) {
    const free = document.getElementById('pf-models');
    if (free) free.value = models || '';
    return;
  }
  picker.querySelectorAll('input[name="model"]').forEach(cb => {
    cb.checked = wanted.has(cb.value);
  });
}

// The default profile cannot be saved as not the default; the server refuses
// it. Its checkbox is locked instead, and since a disabled box is not posted,
// a hidden field keeps sending is_default for it.
function setDefaultLocked(locked) {
  const box = document.getElementById('pf-default');
  box.checked = locked;
  box.disabled = locked;
  document.getElementById('pf-default-keep').disabled = !locked;
  document.getElementById('pf-default-hint').hidden = !locked;
}

function resetProfileForm() {
  document.getElementById('pf-id').value = '';
  document.getElementById('profile-form').reset();
  setDefaultLocked(false);
  setSelectedModels('');
  setQuotaRows([]);
  document.getElementById('profile-form').action = '/admin/profiles';
  document.getElementById('profile-form-title').textContent = PROFILE_TEXT.newTitle;
  document.getElementById('profile-submit-btn').textContent = PROFILE_TEXT.create;
}

// Row menus are popovers, which the browser centres on the screen; this hangs
// each one under the right edge of its ⋮ button instead, and flips it above
// the button when there is no room below. It is placed before it shows, so
// it never flashes in the middle, and corrected once it can be measured.
document.querySelectorAll('.row-menu').forEach(menu => {
  const btn = document.querySelector(`[popovertarget="${menu.id}"]`);
  menu.addEventListener('beforetoggle', e => {
    if (e.newState !== 'open') return;
    const b = btn.getBoundingClientRect();
    menu.style.top = (b.bottom + 4) + 'px';
    menu.style.right = Math.max(8, innerWidth - b.right) + 'px';
  });
  menu.addEventListener('toggle', e => {
    if (e.newState !== 'open') return;
    const b = btn.getBoundingClientRect();
    const m = menu.getBoundingClientRect();
    if (m.bottom > innerHeight - 8) {
      menu.style.top = Math.max(8, b.top - m.height - 4) + 'px';
    }
  });
  // Close before an entry opens a dialog. In the capture phase, so it runs
  // ahead of the entry's own handler: closing afterwards would hand focus
  // back to the ⋮ button behind the dialog.
  menu.addEventListener('click', e => {
    if (e.target.closest('button[type=button]')) menu.hidePopover();
  }, true);
});
// A menu placed on the screen does not follow the page, so it closes instead.
addEventListener('scroll', () => {
  document.querySelectorAll('.row-menu:popover-open').forEach(m => m.hidePopover());
}, true);

// Restore tab from hash
showTab(location.hash.slice(1));
