<script>
    import Field from "@/components/base/Field.svelte";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import { setActivationStatus, activationStatus } from "@/stores/activation";
    import { superuser } from "@/stores/superuser";
    import { addSuccessToast } from "@/stores/toasts";
    import ApiClient from "@/utils/ApiClient";
    import { _, locale } from "svelte-i18n";
    import { onMount } from "svelte";

    export let center = false;

    let classes = "";
    export { classes as class }; // export reserved keyword

    let activationPanel;
    let activationEmail = "";
    let activationCode = "";
    let activationMode = "offline";
    let isSubmittingActivation = false;
    let forceActivation = false;
    let reminderPanel;
    let reminderDisabled = false;
    let reminderSeen = false;

    $: if ($superuser?.email && !activationEmail) {
        activationEmail = $superuser.email;
    }

    $: currentLocale = $locale; // trigger recomputation on locale change
    $: activationStatusLabel = computeActivationLabel($activationStatus, currentLocale);
    $: checkReminder();

    onMount(() => {
        reminderDisabled = readReminderDisabled();
    });

    $: if ($superuser?.id && $activationStatus.requiresActivation) {
        forceActivation = true;
        activationPanel?.show();
    } else if (forceActivation && !$activationStatus.requiresActivation && !isSubmittingActivation) {
        forceActivation = false;
        activationPanel?.hide();
    }

    async function submitActivation() {
        if (!activationEmail || !activationCode || isSubmittingActivation) {
            return;
        }

        isSubmittingActivation = true;

        try {
            const status = await ApiClient.send("/api/activation/verify", {
                method: "POST",
                body: {
                    email: activationEmail,
                    code: activationCode,
                    mode: activationMode,
                },
            });

            setActivationStatus(status);
            addSuccessToast($_("activation.toast.verified"));
            activationCode = "";
            forceActivation = false;
            activationPanel?.hide();
        } catch (err) {
            ApiClient.error(err);
        }

        isSubmittingActivation = false;
    }

    function handleActivationOpen() {
        forceActivation = $activationStatus?.requiresActivation;
        activationPanel?.show();
    }

    function handleActivationClose() {
        if (forceActivation || isSubmittingActivation) {
            return;
        }
        activationPanel?.hide();
    }

    function replaceParam(text, key, value) {
        if (!text || !value) return text;
        return text.replace(`{${key}}`, value);
    }

    function computeActivationLabel(status, localeToken) {
        // localeToken ensures this recomputes when the selected locale changes
        void localeToken;
        if (!status) {
            return $_("activation.label.default");
        }

        const expiryRaw = status.subscriptionExpires || status.trialExpiresAt || "";
        const expiry = expiryRaw ? formatDate(expiryRaw) : "";

        if (status.activated && !status.isExpired && status.subscriptionExpires) {
            if (expiry) {
                const tpl = $_("activation.label.activeUntil", { date: expiry });
                return replaceParam(tpl, "date", expiry);
            }
            return status.message || $_("activation.label.required");
        }

        if (status.isTrial && !status.isExpired && status.trialExpiresAt) {
            if (expiry) {
                const tpl = $_("activation.label.trialExpires", { date: expiry });
                return replaceParam(tpl, "date", expiry);
            }
            return status.message || $_("activation.label.required");
        }

        if (status.isExpired) {
            if (expiry) {
                const tpl = $_("activation.label.expiredOn", { date: expiry });
                return replaceParam(tpl, "date", expiry);
            }
            return status.message || $_("activation.label.expired");
        }

        return status.message || $_("activation.label.required");
    }

    function formatDate(raw) {
        if (!raw) {
            return "";
        }
        const parsed = new Date(raw);
        if (isNaN(parsed)) {
            return raw;
        }
        return parsed.toLocaleString();
    }

    function expiryDate(status) {
        if (!status) return null;
        if (status.subscriptionExpires) {
            return new Date(status.subscriptionExpires);
        }
        if (status.trialExpiresAt) {
            return new Date(status.trialExpiresAt);
        }
        return null;
    }

    function daysUntil(date) {
        if (!date) return null;
        const now = new Date();
        const ms = date.getTime() - now.getTime();
        return Math.floor(ms / (1000 * 60 * 60 * 24));
    }

    function checkReminder() {
        if (!$superuser?.id || reminderDisabled || reminderSeen) {
            return;
        }

        const exp = expiryDate($activationStatus);
        if (!exp || isNaN(exp)) return;

        const remaining = daysUntil(exp);
        if (remaining === null) return;

        const expired = $activationStatus?.isExpired;
        if (expired) return;

        if (remaining <= 3 && remaining >= 0) {
            reminderPanel?.show();
            reminderSeen = true;
        }
    }

    function disableReminder() {
        reminderDisabled = true;
        reminderSeen = true;
        writeReminderDisabled(true);
        reminderPanel?.hide();
    }

    function snoozeReminder() {
        reminderSeen = true;
        reminderPanel?.hide();
    }

    function writeReminderDisabled(value) {
        if (typeof localStorage === "undefined") return;
        localStorage.setItem("pb_activation_reminder_disabled", value ? "1" : "");
    }

    function readReminderDisabled() {
        if (typeof localStorage === "undefined") return false;
        return localStorage.getItem("pb_activation_reminder_disabled") === "1";
    }

    function openActivationFromReminder() {
        reminderPanel?.hide();
        handleActivationOpen();
    }
</script>

<div class="page-wrapper {classes}" class:center-content={center}>
    <main class="page-content">
        <slot />
    </main>

    <footer class="page-footer">
        <slot name="footer" />

        {#if $superuser?.id}
           
            <span class="delimiter">|</span>
            {#if import.meta.env.PB_VERSION}
                <a
                    href="https://github.com/bosbase/bosbase-enterprise/releases"
                    target="_blank"
                    rel="noopener noreferrer"
                    title="Releases"
                >
                    <span class="txt">BosBase {import.meta.env.PB_VERSION}</span>
                </a>
            {/if}
            <span class="delimiter">|</span>
            <span
                class="active-product link"
                role="button"
                tabindex="0"
                on:click|preventDefault={handleActivationOpen}
                on:keydown={(e) => (e.key === "Enter" || e.key === " ") && handleActivationOpen()}
            >
                {activationStatusLabel}
            </span>
        {/if}
    </footer>
</div>

<OverlayPanel
    bind:this={activationPanel}
    class="overlay-panel-sm activation-panel"
    overlayClose={!forceActivation && !isSubmittingActivation}
    btnClose={!forceActivation && !isSubmittingActivation}
    escClose={!forceActivation && !isSubmittingActivation}
    popup
>
    <svelte:fragment slot="header">
        <h4 class="center txt-break">{$_("activation.modal.title")}</h4>
    </svelte:fragment>

    <div class="activation-status m-b-2">
        <p class="m-0">{activationStatusLabel}</p>
        {#if $activationStatus.activationEmail}
            <p class="txt-muted m-0">
                {replaceParam(
                    $_("activation.modal.activatedEmail", {
                        email: $activationStatus.activationEmail || "",
                    }),
                    "email",
                    $activationStatus.activationEmail || "",
                )}
            </p>
        {/if}
    </div>

    <form class="activation-form" autocomplete="off" on:submit|preventDefault={submitActivation}>
        <Field class="form-field required" name="email" let:uniqueId>
            <label for={uniqueId}>{$_("activation.modal.email")}</label>
            <input
                id={uniqueId}
                type="email"
                required
                bind:value={activationEmail}
                disabled={isSubmittingActivation}
            />
        </Field>

        <Field class="form-field required" name="code" let:uniqueId>
            <label for={uniqueId}>{$_("activation.modal.code")}</label>
            <textarea
                id={uniqueId}
                rows="4"
                required
                bind:value={activationCode}
                disabled={isSubmittingActivation}
            />
        </Field>

        <Field class="form-field required" name="mode" let:uniqueId>
            <label for={uniqueId}>{$_("activation.modal.mode")}</label>
            <select
                id={uniqueId}
                bind:value={activationMode}
                disabled={isSubmittingActivation}
            >
                <option value="offline">{$_("activation.modal.modeOffline")}</option>
                <option value="online">{$_("activation.modal.modeOnline")}</option>
            </select>
            <p class="txt-muted m-0">{$_("activation.modal.modeHelp")}</p>
        </Field>
    </form>

    <svelte:fragment slot="footer">
        <button
            type="button"
            class="btn btn-transparent"
            on:click={handleActivationClose}
            disabled={forceActivation || isSubmittingActivation}
        >
            {$_("common.action.close")}
        </button>
        <button
            type="submit"
            class="btn btn-expanded"
            class:btn-loading={isSubmittingActivation}
            on:click={submitActivation}
            disabled={!activationEmail || !activationCode || isSubmittingActivation}
        >
            <span class="txt">{$_("activation.modal.activateNow")}</span>
        </button>
    </svelte:fragment>
</OverlayPanel>

<OverlayPanel
    bind:this={reminderPanel}
    class="overlay-panel-sm activation-panel"
    overlayClose
    btnClose
    escClose
    popup
>
    <svelte:fragment slot="header">
        <h4 class="center txt-break">{$_("activation.reminder.title")}</h4>
    </svelte:fragment>

    <div class="activation-status m-b-2">
        <p class="m-0">
            {#if $activationStatus.subscriptionExpires}
                {$_("activation.reminder.subscription", {
                    date: formatDate($activationStatus.subscriptionExpires),
                })}
            {:else if $activationStatus.trialExpiresAt}
                {$_("activation.reminder.trial", {
                    date: formatDate($activationStatus.trialExpiresAt),
                })}
            {:else}
                {$_("activation.reminder.generic")}
            {/if}
        </p>
        <p class="txt-muted m-0">{$_("activation.reminder.cta")}</p>
    </div>

    <svelte:fragment slot="footer">
        <button type="button" class="btn btn-transparent" on:click={snoozeReminder}>
            {$_("activation.reminder.snooze")}
        </button>
        <button type="button" class="btn btn-transparent" on:click={disableReminder}>
            {$_("activation.reminder.disable")}
        </button>
        <button type="button" class="btn btn-expanded" on:click={openActivationFromReminder}>
            <span class="txt">{$_("activation.reminder.activateNow")}</span>
        </button>
    </svelte:fragment>
</OverlayPanel>

<style>
    .active-product.link {
        cursor: pointer;
        text-decoration: underline;
    }

    .active-product {
        color: #d92c2c;
    }

    .activation-status {
        font-size: 0.9rem;
    }

    .txt-muted {
        color: #777;
    }

    .activation-panel .btn-transparent {
        color: #555;
    }
</style>
