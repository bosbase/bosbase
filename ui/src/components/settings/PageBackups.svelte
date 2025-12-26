<script>
    import { _ } from "svelte-i18n";
    import { slide } from "svelte/transition";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { pageTitle } from "@/stores/app";
    import { removeError, setErrors } from "@/stores/errors";
    import { addSuccessToast } from "@/stores/toasts";
    import tooltip from "@/actions/tooltip";
    import PageWrapper from "@/components/base/PageWrapper.svelte";
    import Field from "@/components/base/Field.svelte";
    import Toggler from "@/components/base/Toggler.svelte";
    import RefreshButton from "@/components/base/RefreshButton.svelte";
    import SettingsSidebar from "@/components/settings/SettingsSidebar.svelte";
    import BackupsList from "@/components/settings/BackupsList.svelte";
    import S3Fields from "@/components/settings/S3Fields.svelte";
    import BackupUploadButton from "@/components/settings/BackupUploadButton.svelte";

    $pageTitle = $_("common.menu.backupConfig");

    let backupsListComponent;
    let originalFormSettings = {};
    let formSettings = {};
    let isLoading = false;
    let isSaving = false;
    let initialHash = "";
    let enableAutoBackups = false;
    let showBackupsSettings = false;
    let isTesting = false;
    let testError = null;

    $: initialHash = JSON.stringify(originalFormSettings);

    $: hasChanges = initialHash != JSON.stringify(formSettings);

    $: if (!enableAutoBackups && formSettings?.backups?.cron) {
        removeError("backups.cron");
        formSettings.backups.cron = "";
    }

    loadSettings();

    async function loadSettings() {
        isLoading = true;

        try {
            const settings = (await ApiClient.settings.getAll()) || {};
            init(settings);
        } catch (err) {
            ApiClient.error(err);
        }

        isLoading = false;
    }

    async function save() {
        if (isSaving || !hasChanges) {
            return;
        }

        isSaving = true;

        try {
            const settings = await ApiClient.settings.update(CommonHelper.filterRedactedProps(formSettings));

            setErrors({});

            await refreshList();

            init(settings);

            addSuccessToast($_("common.message.applyNewSetting"));
        } catch (err) {
            ApiClient.error(err);
        }

        isSaving = false;
    }

    function init(settings = {}) {
        formSettings = {
            backups: settings?.backups || {},
        };

        enableAutoBackups = formSettings.backups.cron != "";

        originalFormSettings = JSON.parse(JSON.stringify(formSettings));
    }

    function reset() {
        formSettings = JSON.parse(JSON.stringify(originalFormSettings || { backups: {} }));
        enableAutoBackups = formSettings.backups.cron != "";
    }

    async function refreshList() {
        return backupsListComponent?.loadBackups();
    }
</script>

<SettingsSidebar />

<PageWrapper>
    <header class="page-header">
        <nav class="breadcrumbs">
            <div class="breadcrumb-item">{$_("common.menu.setting")}</div>
            <div class="breadcrumb-item">{$pageTitle}</div>
        </nav>
    </header>

    <div class="wrapper">
        <div class="panel" autocomplete="off" on:submit|preventDefault={save}>
            <div class="flex m-b-sm flex-gap-10">
                <span class="txt-xl">{$_("page.setting.content.backup.content.1")}</span>
                <RefreshButton
                    class="btn-sm"
                    tooltip={$_("common.action.refresh")}
                    on:refresh={refreshList}
                />
                <BackupUploadButton class="btn-sm" on:success={refreshList} />
            </div>

            <BackupsList bind:this={backupsListComponent} />

            <hr />

            <button
                type="button"
                class="btn btn-secondary"
                class:btn-loading={isLoading}
                disabled={isLoading}
                on:click={() => (showBackupsSettings = !showBackupsSettings)}
            >
                <span class="txt">{$_("page.setting.content.backup.action.moreBackupOption")}</span>
                {#if showBackupsSettings}
                    <i class="ri-arrow-up-s-line" />
                {:else}
                    <i class="ri-arrow-down-s-line" />
                {/if}
            </button>

            {#if showBackupsSettings && !isLoading}
                <form
                    class="block"
                    autocomplete="off"
                    on:submit|preventDefault={save}
                    transition:slide={{ duration: 150 }}
                >
                    <Field class="form-field form-field-toggle m-t-base m-b-0" let:uniqueId>
                        <input type="checkbox" id={uniqueId} bind:checked={enableAutoBackups} />
                        <label for={uniqueId}
                            >{$_("page.setting.content.backup.action.enableAutoBackup")}</label
                        >
                    </Field>

                    {#if enableAutoBackups}
                        <div class="block" transition:slide={{ duration: 150 }}>
                            <div class="grid p-t-base p-b-sm">
                                <div class="col-lg-6">
                                    <Field class="form-field required" name="backups.cron" let:uniqueId>
                                        <label for={uniqueId}
                                            >{$_("page.setting.content.backup.cronExpression")}</label
                                        >
                                        <!-- svelte-ignore a11y-autofocus -->
                                        <input
                                            required
                                            type="text"
                                            id={uniqueId}
                                            class="txt-lg txt-mono"
                                            placeholder="* * * * *"
                                            autofocus={!originalFormSettings?.backups?.cron}
                                            bind:value={formSettings.backups.cron}
                                        />
                                        <div class="form-field-addon">
                                            <button type="button" class="btn btn-sm btn-outline p-r-0">
                                                <span class="txt">{$_("common.placeholder.preset")}</span>
                                                <i class="ri-arrow-drop-down-fill" />
                                                <Toggler class="dropdown dropdown-nowrap dropdown-right">
                                                    <button
                                                        type="button"
                                                        class="dropdown-item closable"
                                                        on:click={() => {
                                                            formSettings.backups.cron = "0 0 * * *";
                                                        }}
                                                    >
                                                        <span class="txt"
                                                            >{$_(
                                                                "page.setting.content.backup.content.14",
                                                            )}</span
                                                        >
                                                    </button>
                                                    <button
                                                        type="button"
                                                        class="dropdown-item closable"
                                                        on:click={() => {
                                                            formSettings.backups.cron = "0 0 * * 0";
                                                        }}
                                                    >
                                                        <span class="txt"
                                                            >{$_(
                                                                "page.setting.content.backup.content.15",
                                                            )}</span
                                                        >
                                                    </button>
                                                    <button
                                                        type="button"
                                                        class="dropdown-item closable"
                                                        on:click={() => {
                                                            formSettings.backups.cron = "0 0 * * 1,3";
                                                        }}
                                                    >
                                                        <span class="txt"
                                                            >{$_(
                                                                "page.setting.content.backup.content.16",
                                                            )}</span
                                                        >
                                                    </button>
                                                    <button
                                                        type="button"
                                                        class="dropdown-item closable"
                                                        on:click={() => {
                                                            formSettings.backups.cron = "0 0 1 * *";
                                                        }}
                                                    >
                                                        <span class="txt"
                                                            >{$_("page.setting.content.backup.content.17")}
                                                        </span>
                                                    </button>
                                                </Toggler>
                                            </button>
                                        </div>
                                        <div class="help-block">
                                            <!-- prettier-ignore -->
                                            <p>
                                                {$_("page.setting.content.backup.content.7")}
                                                <span
                                                    class="link-primary"
                                                    use:tooltip={"@yearly\n@annually\n@monthly\n@weekly\n@daily\n@midnight\n@hourly"}
                                                >{$_("common.placeholder.hoverToSee")}</span>
                                            </p>
                                        </div>
                                    </Field>
                                </div>
                                <div class="col-lg-6">
                                    <Field
                                        class="form-field required"
                                        name="backups.cronMaxKeep"
                                        let:uniqueId
                                    >
                                        <label for={uniqueId}
                                            >{$_("page.setting.content.backup.maxBackupFilesNum")}</label
                                        >
                                        <input
                                            type="number"
                                            id={uniqueId}
                                            min="1"
                                            bind:value={formSettings.backups.cronMaxKeep}
                                        />
                                    </Field>
                                </div>
                            </div>
                        </div>
                    {/if}

                    <div class="clearfix m-b-base" />

                    <S3Fields
                        toggleLabel={$_("page.setting.content.backup.action.backupToS3")}
                        testFilesystem="backups"
                        configKey="backups.s3"
                        originalConfig={originalFormSettings.backups?.s3}
                        bind:config={formSettings.backups.s3}
                        bind:isTesting
                        bind:testError
                    />

                    <div class="flex">
                        <div class="flex-fill" />

                        {#if formSettings.backups?.s3?.enabled && !hasChanges && !isSaving}
                            {#if isTesting}
                                <span class="loader loader-sm" />
                            {:else if testError}
                                <div
                                    class="label label-sm label-warning entrance-right"
                                    use:tooltip={testError.data?.message}
                                >
                                    <i class="ri-error-warning-line txt-warning" />
                                    <span class="txt">Failed to establish S3 connection</span>
                                </div>
                            {:else}
                                <div class="label label-sm label-success entrance-right">
                                    <i class="ri-checkbox-circle-line txt-success" />
                                    <span class="txt">S3 connected successfully</span>
                                </div>
                            {/if}
                        {/if}

                        {#if hasChanges}
                            <button
                                type="button"
                                class="btn btn-hint btn-transparent"
                                disabled={!hasChanges || isSaving}
                                on:click={() => reset()}
                            >
                                <span class="txt">{$_("common.action.reset")}</span>
                            </button>
                        {/if}

                        <button
                            type="submit"
                            class="btn btn-expanded"
                            class:btn-loading={isSaving}
                            disabled={!hasChanges || isSaving}
                            on:click={() => save()}
                        >
                            <span class="txt">{$_("common.action.save")}</span>
                        </button>
                    </div>
                </form>
            {/if}
        </div>
    </div>
</PageWrapper>
