<script>
    import { _ } from "svelte-i18n";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { setErrors } from "@/stores/errors";
    import { addErrorToast } from "@/stores/toasts";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import Field from "@/components/base/Field.svelte";
    import CopyIcon from "@/components/base/CopyIcon.svelte";
    import { onDestroy } from "svelte";

    const formId = "backup_restore_" + CommonHelper.randomString(5);

    let panel;
    let name = "";
    let nameConfirm = "";
    let isSubmitting = false;
    let reloadTimeoutId = null;

    $: canSubmit = nameConfirm != "" && name == nameConfirm;

    export function show(backupName) {
        setErrors({});
        nameConfirm = "";
        name = backupName;
        isSubmitting = false;
        panel?.show();
    }

    export function hide() {
        return panel?.hide();
    }

    async function submit() {
        if (!canSubmit || isSubmitting) {
            return;
        }

        clearTimeout(reloadTimeoutId);

        isSubmitting = true;

        try {
            await ApiClient.backups.restore(name);

            // optimistic restore page reload
            reloadTimeoutId = setTimeout(() => {
                window.location.reload();
            }, 2000);
        } catch (err) {
            clearTimeout(reloadTimeoutId);

            if (!err?.isAbort) {
                isSubmitting = false;
                addErrorToast(err.response?.message || err.message);
            }
        }
    }

    onDestroy(() => {
        clearTimeout(reloadTimeoutId);
    });
</script>

<OverlayPanel
    bind:this={panel}
    class="backup-restore-panel"
    overlayClose={!isSubmitting}
    escClose={!isSubmitting}
    beforeHide={() => !isSubmitting}
    popup
    on:show
    on:hide
>
    <svelte:fragment slot="header">
        <h4 class="popup-title txt-ellipsis">
            {$_("page.setting.content.backup.action.restoreBackup")}: “<strong>{name}</strong>”
        </h4>
    </svelte:fragment>

    <div class="alert alert-danger">
        <div class="icon">
            <i class="ri-alert-line" />
        </div>
        <div class="content">
            <p class="txt-bold">{$_("page.setting.content.backup.content.8")}</p>

            <p>{$_("page.setting.content.backup.content.9")}</p>
            <p>
                {$_("page.setting.content.backup.content.10")}
            </p>
            <p>{$_("page.setting.content.backup.content.11")}</p>
            <p>
                {$_("page.setting.content.backup.content.12")}
            </p>
        </div>
    </div>

    <div class="content m-b-xs">
        {$_("page.setting.content.backup.content.13")}:
        <div class="label">
            <span class="txt">{name}</span>
            <CopyIcon value={name} />
        </div>
    </div>

    <form id={formId} autocomplete="off" on:submit|preventDefault={submit}>
        <Field class="form-field required m-0" name="name" let:uniqueId>
            <label for={uniqueId}>{$_("common.placeholder.secondaryValidationInput")}</label>
            <input type="text" id={uniqueId} required bind:value={nameConfirm} />
        </Field>
    </form>

    <svelte:fragment slot="footer">
        <button type="button" class="btn btn-transparent" on:click={hide} disabled={isSubmitting}>
            {$_("common.action.close")}
        </button>
        <button
            type="submit"
            form={formId}
            class="btn btn-expanded"
            class:btn-loading={isSubmitting}
            disabled={!canSubmit || isSubmitting}
        >
            <span class="txt">{$_("common.action.start")}{$_("common.action.restore")}</span>
        </button>
    </svelte:fragment>
</OverlayPanel>

<style>
    .popup-title {
        max-width: 80%;
    }
</style>
