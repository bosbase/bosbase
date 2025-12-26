<!-- 代理IP相关 -->
<script>
    import { _ } from "svelte-i18n";
    import tooltip from "@/actions/tooltip";
    import Accordion from "@/components/base/Accordion.svelte";
    import Field from "@/components/base/Field.svelte";
    import MultipleValueInput from "@/components/base/MultipleValueInput.svelte";
    import ObjectSelect from "@/components/base/ObjectSelect.svelte";
    import { errors } from "@/stores/errors";
    import CommonHelper from "@/utils/CommonHelper";
    import { scale } from "svelte/transition";

    const commonProxyHeaders = ["X-Forward-For", "Fly-Client-IP", "CF-Connecting-IP"];

    export let formSettings;
    export let healthData;

    let initialSettingsHash = "";

    $: settingsHash = JSON.stringify(formSettings);

    $: if (initialSettingsHash != settingsHash) {
        initialSettingsHash = settingsHash;
    }

    $: hasChanges = initialSettingsHash != settingsHash;

    $: hasErrors = !CommonHelper.isEmpty($errors?.trustedProxy);

    $: isEnabled = !CommonHelper.isEmpty(formSettings.trustedProxy.headers);

    $: suggestedProxyHeaders = !healthData.possibleProxyHeader
        ? commonProxyHeaders
        : [healthData.possibleProxyHeader].concat(
              commonProxyHeaders.filter((h) => h != healthData.possibleProxyHeader),
          );

    function setHeader(val) {
        formSettings.trustedProxy.headers = [val];
    }

    const ipOptions = [
        { label: $_("page.setting.content.application.proxy.tip.3"), value: true },
        { label: $_("page.setting.content.application.proxy.tip.4"), value: false },
    ];
</script>

<Accordion single>
    <svelte:fragment slot="header">
        <div class="inline-flex">
            <i class="ri-route-line"></i>
            <span class="txt">{$_("page.setting.content.application.proxy.title")}</span>
            {#if !isEnabled && healthData.possibleProxyHeader}
                <i
                    class="ri-alert-line txt-sm txt-warning"
                    use:tooltip={"page.setting.content.application.proxy.tip.1"}
                />
            {:else if isEnabled && !hasChanges && !formSettings.trustedProxy.headers.includes(healthData.possibleProxyHeader)}
                <i
                    class="ri-alert-line txt-sm txt-hint"
                    use:tooltip={$_("page.setting.content.application.proxy.tip.1")}
                />
            {/if}
        </div>

        <div class="flex-fill" />

        {#if isEnabled}
            <span class="label label-success">{$_("common.tip.enabled")}</span>
        {:else}
            <span class="label">{$_("common.tip.disabled")}</span>
        {/if}

        {#if hasErrors}
            <i
                class="ri-error-warning-fill txt-danger"
                transition:scale={{ duration: 150, start: 0.7 }}
                use:tooltip={{ text: "Has errors", position: "left" }}
            />
        {/if}
    </svelte:fragment>

    <div class="alert alert-info m-b-sm">
        <div class="content">
            <div class="inline-flex flex-gap-5">
                <span>{$_("page.setting.content.application.proxy.curIp")}:</span>
                <strong>{healthData.realIP || "N/A"}</strong>
                <i
                    class="ri-information-line txt-sm link-hint"
                    use:tooltip={$_("page.setting.content.application.proxy.tip.5")}
                />
            </div>
            <br />
            <div class="inline-flex flex-gap-5">
                <span>{$_("page.setting.content.application.proxy.curProxyHeader")}:</span>
                <strong>{healthData.possibleProxyHeader || $_("page.setting.content.application.proxy.content.8")}</strong>
            </div>
        </div>
    </div>

    <div class="content m-b-sm">
        <p>
            {$_("page.setting.content.application.proxy.content.1")}
        </p>
        <p>
            {$_("page.setting.content.application.proxy.content.2")}
        </p>
        <p class="txt-bold">{$_("page.setting.content.application.proxy.content.3")}：</p>
        <ul class="m-t-0 txt-bold">
            <li>{$_("page.setting.content.application.proxy.content.4")}</li>
            <li>{$_("page.setting.content.application.proxy.content.5")}</li>
        </ul>
        <p>{$_("page.setting.content.application.proxy.content.6")}</p>
    </div>

    <div class="grid grid-sm">
        <div class="col-lg-9">
            <Field class="form-field m-b-0" name="trustedProxy.headers" let:uniqueId>
                <label for={uniqueId}>{$_("page.setting.content.application.proxy.trustedHeaders")}</label>
                <MultipleValueInput
                    id={uniqueId}
                    placeholder={$_("common.placeholder.defaultSetDisable")}
                    bind:value={formSettings.trustedProxy.headers}
                />
                <div class="form-field-addon">
                    <button
                        type="button"
                        class="btn btn-sm btn-hint btn-transparent btn-clear"
                        class:hidden={CommonHelper.isEmpty(formSettings.trustedProxy.headers)}
                        on:click={() => (formSettings.trustedProxy.headers = [])}
                    >
                        {$_("common.action.clear")}
                    </button>
                </div>
                <div class="help-block">
                    <p>
                        {$_("page.setting.content.application.proxy.content.7")}
                        {#each suggestedProxyHeaders as header}
                            <button
                                type="button"
                                class="label label-sm link-primary txt-mono"
                                on:click={() => setHeader(header)}
                            >
                                {header}
                            </button>&nbsp;
                        {/each}
                    </p>
                </div>
            </Field>
        </div>
        <div class="col-lg-3">
            <Field class="form-field m-0" name="trustedProxy.useLeftmostIP" let:uniqueId>
                <label for={uniqueId}>
                    <span class="txt">{$_("page.setting.content.application.proxy.priorityIp")}</span>
                    <i
                        class="ri-information-line link-hint"
                        use:tooltip={{
                            text: $_("page.setting.content.application.proxy.tip.2"),
                            position: "right",
                        }}
                    />
                </label>
                <ObjectSelect
                    items={ipOptions}
                    bind:keyOfSelected={formSettings.trustedProxy.useLeftmostIP}
                />
            </Field>
        </div>
    </div>
</Accordion>
