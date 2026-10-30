// Help tooltips.
//
// Positioned in the top layer via the popover API rather than as a CSS
// ::after: the profiles table lives inside an overflow-x scroll container,
// which clips absolutely-positioned children no matter how they are anchored.
//
// Any element with a data-help attribute gets one: the help buttons, and the
// bars of the usage chart, which show their day's count.
(function () {
  let popover = null;
  let current = null;
  let hideTimer = null;

  // Leaving a button hides its popover only after a grace period, so the
  // pointer can cross the gap onto the popover and stay there, as screen
  // magnifier users need to read it (WCAG 1.4.13).
  const hideDelay = 300;

  function ensurePopover() {
    if (popover) return popover;
    popover = document.createElement('div');
    popover.className = 'help-popover';
    popover.popover = 'manual';
    popover.addEventListener('mouseenter', cancelHide);
    popover.addEventListener('mouseleave', hideSoon);
    document.body.appendChild(popover);
    return popover;
  }

  function show(trigger) {
    cancelHide();
    const text = trigger.getAttribute('data-help');
    if (!text) return;

    const el = ensurePopover();
    current = trigger;
    el.textContent = text;
    el.showPopover();

    // Measure after showing, then keep the box inside the viewport.
    const t = trigger.getBoundingClientRect();
    const p = el.getBoundingClientRect();
    const gap = 8;

    let left = t.left + t.width / 2 - p.width / 2;
    left = Math.max(gap, Math.min(left, window.innerWidth - p.width - gap));

    // Prefer above; flip below when there is not room.
    let top = t.top - p.height - gap;
    if (top < gap) top = t.bottom + gap;

    el.style.left = left + 'px';
    el.style.top = top + 'px';
  }

  function cancelHide() {
    clearTimeout(hideTimer);
    hideTimer = null;
  }

  function hideSoon() {
    cancelHide();
    hideTimer = setTimeout(hide, hideDelay);
  }

  function hide() {
    cancelHide();
    if (popover && popover.matches(':popover-open')) popover.hidePopover();
  }

  function isOpen() {
    return popover !== null && popover.matches(':popover-open');
  }

  function bind(el) {
    // Only a mouse hovers: a tap sends mouseenter too, just before its click.
    el.addEventListener('pointerenter', (e) => { if (e.pointerType === 'mouse') show(el); });
    el.addEventListener('pointerleave', (e) => { if (e.pointerType === 'mouse') hideSoon(); });
    el.addEventListener('focus', () => show(el));
    el.addEventListener('blur', hide);
    // Touch has no hover; tapping toggles. A tap focuses a button, which opens
    // the popover before the click arrives, so the click goes by the state
    // before the press. Tapping another trigger, such as the next bar of the
    // chart, moves the popover there instead. A keyboard click (detail 0) has
    // no press.
    let openBefore = false;
    el.addEventListener('pointerdown', () => { openBefore = current === el && isOpen(); });
    el.addEventListener('click', (e) => {
      e.preventDefault();
      const open = e.detail === 0 ? current === el && isOpen() : openBefore;
      if (open) hide();
      else show(el);
    });
  }

  document.querySelectorAll('[data-help]').forEach(bind);
  document.addEventListener('keydown', (e) => { if (e.key === 'Escape') hide(); });
  window.addEventListener('scroll', hide, true);
})();
