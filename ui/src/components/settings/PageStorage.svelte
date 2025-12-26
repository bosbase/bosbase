<script>
    import { _ } from "svelte-i18n";
    import { slide } from "svelte/transition";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { pageTitle } from "@/stores/app";
    import { setErrors } from "@/stores/errors";
    import { removeAllToasts, addWarningToast, addSuccessToast } from "@/stores/toasts";
    import tooltip from "@/actions/tooltip";
    import PageWrapper from "@/components/base/PageWrapper.svelte";
    import SettingsSidebar from "@/components/settings/SettingsSidebar.svelte";
    import S3Fields from "@/components/settings/S3Fields.svelte";

    $pageTitle = $_("common.menu.storageConfig");

    const testRequestKey = "s3_test_request";
    const healthRequestKey = "storage_health_request";

    let originalFormSettings = {};
    let formSettings = {};
    let isLoading = false;
    let isSaving = false;
    let isTesting = false;
    let testError = null;
    let requireS3 = false;

    $: initialHash = JSON.stringify(originalFormSettings);

    $: hasChanges = initialHash != JSON.stringify(formSettings);

    loadSettings();

    async function loadSettings() {
        isLoading = true;

        try {
            const [settings, health] = await Promise.all([
                ApiClient.settings.getAll(),
                ApiClient.health.check({ $cancelKey: healthRequestKey }).catch(() => null),
            ]);

            requireS3 = !!health?.data?.requireS3;
            init(settings || {});
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
            ApiClient.cancelRequest(testRequestKey);
            const settings = await ApiClient.settings.update(CommonHelper.filterRedactedProps(formSettings));
            setErrors({});

            await init(settings);

            removeAllToasts();

            if (testError) {
                addWarningToast("Successfully saved but failed to establish S3 connection.");
            } else {
                addSuccessToast("Successfully saved files storage settings.");
            }
        } catch (err) {
            ApiClient.error(err);
        }

        isSaving = false;
    }

    async function init(settings = {}) {
        formSettings = {
            s3: settings?.s3 || {},
        };

        originalFormSettings = JSON.parse(JSON.stringify(formSettings));
    }

    async function reset() {
        formSettings = JSON.parse(JSON.stringify(originalFormSettings || {}));
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
        <form class="panel" autocomplete="off" on:submit|preventDefault={() => save()}>
            <div class="content txt-xl m-b-base">
                {#if requireS3}
                    <div class="alert alert-info m-0">
                        <div class="icon">
                            <i class="ri-information-line" />
                        </div>
                        <div class="content">
                            {$_("page.setting.content.fileStorage.content.4")}
                        </div>
                    </div>
                {:else}
                    <p>{$_("page.setting.content.fileStorage.content.1")}</p>
                {/if}
            </div>

            {#if requireS3}
                <div class="alert alert-warning m-b-base">
                    <div class="icon">
                        <i class="ri-error-warning-line" />
                    </div>
                    <div class="content">
                        {$_("page.setting.content.fileStorage.content.2")}
                        <br />
                        {$_("page.setting.content.fileStorage.content.3")}
                        <a
                            href="https://github.com/rclone/rclone"
                            target="_blank"
                            rel="noopener noreferrer"
                            class="txt-bold"
                        >
                            rclone
                        </a>
                        /
                        <a
                            href="https://github.com/peak/s5cmd"
                            target="_blank"
                            rel="noopener noreferrer"
                            class="txt-bold"
                        >
                            s5cmd
                        </a> / etc
                    </div>
                </div>
            {/if}

            {#if isLoading}
                <div class="loader" />
            {:else}
                <S3Fields
                    toggleLabel={$_("page.setting.content.fileStorage.action.s3Enable")}
                    originalConfig={originalFormSettings.s3}
                    bind:config={formSettings.s3}
                    bind:isTesting
                    bind:testError
                    requireS3={requireS3}
                >
                    {#if originalFormSettings.s3?.enabled != formSettings.s3.enabled}
                        <div transition:slide={{ duration: 150 }}>
                            <div class="alert alert-warning m-0">
                                <div class="icon">
                                    <i class="ri-error-warning-line" />
                                </div>
                                <div class="content">
                                    {$_("page.setting.content.fileStorage.content.2")}
                                    <br />
                                    {$_("page.setting.content.fileStorage.content.3")}
                                    <a
                                        href="https://github.com/rclone/rclone"
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        class="txt-bold"
                                    >
                                        rclone
                                    </a>
                                    /
                                    <a
                                        href="https://github.com/peak/s5cmd"
                                        target="_blank"
                                        rel="noopener noreferrer"
                                        class="txt-bold"
                                    >
                                        s5cmd
                                    </a> / etc
                                </div>
                            </div>
                            <div class="clearfix m-t-base" />
                        </div>
                    {/if}
                </S3Fields>

                <div class="flex">
                    <div class="flex-fill" />

                    {#if formSettings.s3?.enabled && !hasChanges && !isSaving}
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
                            class="btn btn-transparent btn-hint"
                            disabled={isSaving}
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
            {/if}
        </form>
    </div>
</PageWrapper>
