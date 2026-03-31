const dataEl = document.getElementById('budget-data');
let participantsByItem = {};
let itemCosts = {};
if (dataEl) {
    try {
        const parsed = JSON.parse(dataEl.textContent.trim() || '{}');
        participantsByItem = parsed.participants || {};
        itemCosts = parsed.itemCosts || {};
    } catch (err) {
        console.error('unable to read budget data', err);
    }
}
const tabs = document.querySelectorAll('.tab');
const panels = document.querySelectorAll('.tab-panel');
const itemSelect = document.querySelector('select[name="item"]');
const payerSelect = document.querySelector('select[name="user"]');
const amountInput = document.querySelector('input[name="amount"]');
const paymentForm = document.querySelector('form[action="/payment"]');
const preferredTab = document.body.dataset.activeTab || 'budget-tab';

const showPanel = target => {
    panels.forEach(panel => panel.classList.toggle('active', panel.id === target));
    tabs.forEach(tab => tab.classList.toggle('active', tab.dataset.target === target));
};

tabs.forEach(tab => tab.addEventListener('click', () => showPanel(tab.dataset.target)));

const populatePayers = () => {
    if (!payerSelect || !itemSelect) return;
    const participants = participantsByItem[itemSelect.value] || [];
    payerSelect.innerHTML = '';
    if (participants.length === 0) {
        const option = document.createElement('option');
        option.disabled = true;
        option.textContent = 'No participants for this item';
        payerSelect.appendChild(option);
        return;
    }
    participants.forEach(name => {
        const option = document.createElement('option');
        option.value = name;
        option.textContent = name;
        payerSelect.appendChild(option);
    });
};

const currentItemCost = () => {
    if (!itemSelect) return 0;
    return Number(itemCosts[itemSelect.value]) || 0;
};

const updateAmountLimit = () => {
    if (!amountInput) return;
    const limit = currentItemCost();
    if (limit > 0) {
        amountInput.max = limit.toFixed(2);
    } else {
        amountInput.removeAttribute('max');
    }
};

const validatePaymentAmount = () => {
    if (!paymentForm || !amountInput) return true;
    const limit = currentItemCost();
    const value = parseFloat(amountInput.value);
    if (Number.isNaN(value)) return true;
    if (limit > 0 && value > limit) {
        alert(`Amount cannot exceed ${limit.toFixed(2)}`);
        return false;
    }
    return true;
};

itemSelect?.addEventListener('change', () => {
    populatePayers();
    updateAmountLimit();
});

paymentForm?.addEventListener('submit', event => {
    if (!validatePaymentAmount()) {
        event.preventDefault();
    }
});

const setupWarningOverlay = () => {
    const overlay = document.getElementById('warning-overlay');
    if (!overlay) return;
    const close = () => {
        overlay.classList.remove('visible');
        const url = new URL(window.location);
        url.searchParams.delete('userWarning');
        window.history.replaceState({}, '', url);
    };
    document.getElementById('warning-ok')?.addEventListener('click', close);
    setTimeout(close, 10000);
};

document.addEventListener('DOMContentLoaded', () => {
    showPanel(preferredTab);
    populatePayers();
    updateAmountLimit();
    setupWarningOverlay();
});
