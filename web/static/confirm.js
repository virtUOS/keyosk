// Forms that ask before they are sent carry the question in data-confirm.
//
// Listening on the document in the capture phase runs this ahead of the
// form's own submit listeners, so a declined question reaches them as
// defaultPrevented and they leave the form as it was.
document.addEventListener('submit', e => {
  const form = e.target.closest('form[data-confirm]');
  if (form && !confirm(form.dataset.confirm)) e.preventDefault();
}, true);
