<script>
    import { _ } from "svelte-i18n";
    import { createEventDispatcher, onDestroy } from "svelte";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { setErrors } from "@/stores/errors";
    import { addInfoToast, addSuccessToast } from "@/stores/toasts";
    import OverlayPanel from "@/components/base/OverlayPanel.svelte";
    import Field from "@/components/base/Field.svelte";

    const dispatch = createEventDispatcher();

    const formId = "backup_create_" + CommonHelper.randomString(5);

    let panel;
    let name = "";
    let isSubmitting = false;
    let submitTimeoutId;

    export function show(newName) {
        setErrors({});
        isSubmitting = false;
        name = newName || "";
        panel?.show();
    }

    export function hide() {
        return panel?.hide();
    }

    async function submit() {
        if (isSubmitting) {
            return;
        }

        isSubmitting = true;

        clearTimeout(submitTimeoutId);
        submitTimeoutId = setTimeout(() => {
            hide();
        }, 1500);

        try {
            await ApiClient.backups.create(name, { $cancelKey: formId });

            isSubmitting = false;

            hide();
            dispatch("submit");
            addSuccessToast($_("page.setting.content.backup.content.19"));
        } catch (err) {
            if (!err.isAbort) {
                ApiClient.error(err);
            }
        }

        clearTimeout(submitTimeoutId);
        isSubmitting = false;
    }

    onDestroy(() => {
        clearTimeout(submitTimeoutId);
    });
</script>

<OverlayPanel
    bind:this={panel}
    class="backup-create-panel"
    beforeOpen={() => {
        if (isSubmitting) {
            addInfoToast("A backup has already been started, please wait.");
            return false;
        }

        return true;
    }}
    beforeHide={() => {
        if (isSubmitting) {
            addInfoToast(
                "The backup was started but may take a while to complete. You can come back later.",
                4500,
            );
        }

        return true;
    }}
    popup
    on:show
    on:hide
>
    <svelte:fragment slot="header">
        <h4 class="center txt-break">{$_("page.setting.content.backup.action.createBackup")}</h4>
    </svelte:fragment>

    <div class="alert alert-info">
        <div class="icon">
            <i class="ri-information-line" />
        </div>
        <div class="content">
            <p>{$_("page.setting.content.backup.content.3")}
            </p>
            <p class="txt-bold">
                {$_("page.setting.content.backup.content.4")}
            </p>
        </div>
    </div>

    <form id={formId} autocomplete="off" on:submit|preventDefault={submit}>
        <Field class="form-field m-0" name="name" let:uniqueId>
            <label for={uniqueId}>{$_("common.placeholder.fileName")}</label>
            <input
                type="text"
                id={uniqueId}
                placeholder={$_("common.placeholder.autoGenerate")}
                pattern="^[a-z0-9_-]+\.zip$"
                bind:value={name}
            />
            <em class="help-block">{$_("page.setting.content.backup.content.5")}</em>
        </Field>
    </form>

    <svelte:fragment slot="footer">
        <button type="button" class="btn btn-transparent" on:click={hide} disabled={isSubmitting}>
            <span class="txt">{$_("common.action.cancel")}</span>
        </button>
        <button
            type="submit"
            form={formId}
            class="btn btn-expanded"
            class:btn-loading={isSubmitting}
            disabled={isSubmitting}
        >
            <span class="txt">{$_("common.action.start")}{$_("common.action.create")}</span>
        </button>
    </svelte:fragment>
</OverlayPanel>
