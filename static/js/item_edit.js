document.addEventListener('DOMContentLoaded', () => {
    const overlay = document.getElementById('edit-warning');
    if (!overlay) return;
    const close = () => overlay.classList.remove('visible');
    document.getElementById('warning-ok')?.addEventListener('click', close);
    setTimeout(close, 10000);
});
