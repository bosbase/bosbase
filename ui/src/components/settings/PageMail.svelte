<script>
    import { _ } from "svelte-i18n";
    import tooltip from "@/actions/tooltip";
    import Field from "@/components/base/Field.svelte";
    import ObjectSelect from "@/components/base/ObjectSelect.svelte";
    import PageWrapper from "@/components/base/PageWrapper.svelte";
    import RedactedPasswordInput from "@/components/base/RedactedPasswordInput.svelte";
    import EmailTestPopup from "@/components/settings/EmailTestPopup.svelte";
    import SettingsSidebar from "@/components/settings/SettingsSidebar.svelte";
    import { pageTitle } from "@/stores/app";
    import { setErrors } from "@/stores/errors";
    import { addSuccessToast } from "@/stores/toasts";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";
    import { slide } from "svelte/transition";

    const tlsOptions = [
        { label: $_("common.tip.enabled"), value: false },
        { label: $_("common.tip.disabled"), value: true },
    ];

    const authMethods = [
        { label: "PLAIN (default)", value: "PLAIN" },
        { label: "LOGIN", value: "LOGIN" },
    ];

    $pageTitle = $_("common.menu.smtpConfig");

    let testPopup;
    let originalFormSettings = {};
    let formSettings = {};
    let isLoading = false;
    let isSaving = false;
    let maskPassword = false;
    let showMoreOptions = false;

    $: initialHash = JSON.stringify(originalFormSettings);

    $: hasChanges = initialHash != JSON.stringify(formSettings);

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
            init(settings);
            setErrors({});
            addSuccessToast($_("common.message.applyNewSetting"));
        } catch (err) {
            ApiClient.error(err);
        }

        isSaving = false;
    }

    function init(settings = {}) {
        formSettings = {
            meta: settings?.meta || {},
            smtp: settings?.smtp || {},
        };

        formSettings.smtp.enabled = true;

        if (!formSettings.smtp.authMethod) {
            formSettings.smtp.authMethod = authMethods[0].value;
        }

        originalFormSettings = JSON.parse(JSON.stringify(formSettings));

        maskPassword = !!formSettings.smtp.username;
    }

    function reset() {
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
                <p>{$_("page.setting.content.smtp.title")}</p>
            </div>

            {#if isLoading}
                <div class="loader" />
            {:else}
                <div class="grid m-b-base">
                    <div class="col-lg-6">
                        <Field class="form-field required" name="meta.senderName" let:uniqueId>
                            <label for={uniqueId}>{$_("page.setting.content.smtp.senderNickname")}</label>
                            <input
                                type="text"
                                id={uniqueId}
                                required
                                bind:value={formSettings.meta.senderName}
                            />
                        </Field>
                    </div>

                    <div class="col-lg-6">
                        <Field class="form-field required" name="meta.senderAddress" let:uniqueId>
                            <label for={uniqueId}>{$_("page.setting.content.smtp.senderEmail")}</label>
                            <input
                                type="email"
                                id={uniqueId}
                                required
                                bind:value={formSettings.meta.senderAddress}
                            />
                        </Field>
                    </div>
                </div>

                <Field class="form-field form-field-toggle m-b-sm" let:uniqueId>
                    <input type="checkbox" id={uniqueId} required bind:checked={formSettings.smtp.enabled} />
                    <label for={uniqueId}>
                        <span class="txt">{$_("page.setting.content.smtp.action.smtpEnable")}</span>
                        <i
                            class="ri-information-line link-hint"
                            use:tooltip={{
                                text: $_("page.setting.content.smtp.content.1"),
                                position: "top",
                            }}
                        />
                    </label>
                </Field>

                {#if formSettings.smtp.enabled}
                    <div transition:slide={{ duration: 150 }}>
                        <div class="grid">
                            <div class="col-lg-4">
                                <Field class="form-field required" name="smtp.host" let:uniqueId>
                                    <label for={uniqueId}>{$_("page.setting.content.smtp.smtpHost")}</label>
                                    <input
                                        type="text"
                                        id={uniqueId}
                                        required
                                        bind:value={formSettings.smtp.host}
                                    />
                                </Field>
                            </div>
                            <div class="col-lg-2">
                                <Field class="form-field required" name="smtp.port" let:uniqueId>
                                    <label for={uniqueId}>{$_("page.setting.content.smtp.smtpPort")}</label>
                                    <input
                                        type="number"
                                        id={uniqueId}
                                        required
                                        bind:value={formSettings.smtp.port}
                                    />
                                </Field>
                            </div>
                            <div class="col-lg-3">
                                <Field class="form-field" name="smtp.username" let:uniqueId>
                                    <label for={uniqueId}
                                        >{$_("page.setting.content.smtp.smtpUsername")}</label
                                    >
                                    <input
                                        type="text"
                                        id={uniqueId}
                                        bind:value={formSettings.smtp.username}
                                    />
                                </Field>
                            </div>
                            <div class="col-lg-3">
                                <Field class="form-field" name="smtp.password" let:uniqueId>
                                    <label for={uniqueId}
                                        >{$_("page.setting.content.smtp.smtpPassword")}</label
                                    >
                                    <RedactedPasswordInput
                                        id={uniqueId}
                                        bind:mask={maskPassword}
                                        bind:value={formSettings.smtp.password}
                                    />
                                </Field>
                            </div>
                        </div>

                        <button
                            type="button"
                            class="btn btn-sm btn-secondary m-t-sm m-b-sm"
                            on:click|preventDefault={() => {
                                showMoreOptions = !showMoreOptions;
                            }}
                        >
                            {#if showMoreOptions}
                                <span class="txt">{$_("page.setting.content.smtp.action.hideDetail")}</span>
                                <i class="ri-arrow-up-s-line" />
                            {:else}
                                <span class="txt">{$_("page.setting.content.smtp.action.expandDetail")}</span>
                                <i class="ri-arrow-down-s-line" />
                            {/if}
                        </button>

                        {#if showMoreOptions}
                            <div class="grid" transition:slide={{ duration: 150 }}>
                                <div class="col-lg-3">
                                    <Field class="form-field" name="smtp.tls" let:uniqueId>
                                        <label for={uniqueId}
                                            >{$_("page.setting.content.smtp.tlsEncryption")}</label
                                        >
                                        <ObjectSelect
                                            id={uniqueId}
                                            items={tlsOptions}
                                            bind:keyOfSelected={formSettings.smtp.tls}
                                        />
                                    </Field>
                                </div>
                                <div class="col-lg-3">
                                    <Field class="form-field" name="smtp.authMethod" let:uniqueId>
                                        <label for={uniqueId}
                                            >{$_("page.setting.content.smtp.authMethod")}</label
                                        >
                                        <ObjectSelect
                                            id={uniqueId}
                                            items={authMethods}
                                            bind:keyOfSelected={formSettings.smtp.authMethod}
                                        />
                                    </Field>
                                </div>
                                <div class="col-lg-6">
                                    <Field class="form-field" name="smtp.localName" let:uniqueId>
                                        <label for={uniqueId}>
                                            <span class="txt"
                                                >{$_("page.setting.content.smtp.ehloOrHeloDomain")}</span
                                            >
                                            <i
                                                class="ri-information-line link-hint"
                                                use:tooltip={{
                                                    text: $_("page.setting.content.smtp.content.2"),
                                                    position: "top",
                                                }}
                                            />
                                        </label>
                                        <input
                                            type="text"
                                            id={uniqueId}
                                            placeholder={$_("common.message.defaultValue", {
                                                values: { value: "localhost" },
                                            })}
                                            bind:value={formSettings.smtp.localName}
                                        />
                                    </Field>
                                </div>
                                <div class="col-lg-12" />
                            </div>
                        {/if}
                    </div>
                {/if}

                <div class="flex">
                    <div class="flex-fill" />

                    {#if hasChanges}
                        <button
                            type="button"
                            class="btn btn-transparent btn-hint"
                            disabled={isSaving}
                            on:click={() => reset()}
                        >
                            <span class="txt">{$_("common.action.cancel")}</span>
                        </button>
                        <button
                            type="submit"
                            class="btn btn-expanded"
                            class:btn-loading={isSaving}
                            disabled={!hasChanges || isSaving}
                            on:click={() => save()}
                        >
                            <span class="txt">{$_("common.action.save")}</span>
                        </button>
                    {:else}
                        <button
                            type="button"
                            class="btn btn-expanded btn-outline"
                            on:click={() => testPopup?.show()}
                        >
                            <i class="ri-mail-check-line" />
                            <span class="txt">{$_("common.action.sendTestEmail")}</span>
                        </button>
                    {/if}
                </div>
            {/if}
        </form>
    </div>
</PageWrapper>

<EmailTestPopup bind:this={testPopup} />
