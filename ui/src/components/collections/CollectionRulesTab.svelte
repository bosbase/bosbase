<script>
    import { _ } from "svelte-i18n";
    import tooltip from "@/actions/tooltip";
    import RuleField from "@/components/collections/RuleField.svelte";
    import CommonHelper from "@/utils/CommonHelper";
    import { slide } from "svelte/transition";

    export let collection;

    $: fieldNames = CommonHelper.getAllCollectionIdentifiers(collection);

    $: hiddenFieldNames = collection.fields?.filter((f) => f.hidden).map((f) => f.name);

    let showFiltersInfo = false;

    let showExtraRules = collection.manageRule !== null || collection.authRule !== "";
</script>

<div class="block m-b-sm handle">
    <div class="flex txt-sm txt-hint m-b-5">
        <p>
            
        </p>
        <button
            type="button"
            class="expand-handle txt-sm txt-bold txt-nowrap link-hint"
            on:click={() => (showFiltersInfo = !showFiltersInfo)}
        >
            {showFiltersInfo
                ? $_("common.popup.apiRequestPermission.action.hideList")
                : $_("common.popup.apiRequestPermission.action.showList")}
        </button>
    </div>

    {#if showFiltersInfo}
        <div transition:slide={{ duration: 150 }}>
            <div class="alert alert-warning m-0">
                <div class="content">
                    <p class="m-b-0">{$_("common.popup.apiDocs.content.1")}</p>
                    <div class="inline-flex flex-gap-5">
                        {#each fieldNames as name}
                            {#if !hiddenFieldNames.includes(name)}
                                <code>{name}</code>
                            {/if}
                        {/each}
                    </div>

                    <hr class="m-t-10 m-b-5" />

                    <p class="m-b-0">{$_("common.popup.apiDocs.content.2")}</p>
                    <div class="inline-flex flex-gap-5">
                        <code>@request.headers.*</code>
                        <code>@request.query.*</code>
                        <code>@request.body.*</code>
                        <code>@request.auth.*</code>
                    </div>

                    <hr class="m-t-10 m-b-5" />

                    <p class="m-b-0">{$_("common.popup.apiDocs.content.3")}
                    </p>
                    <div class="inline-flex flex-gap-5">
                        <code>@collection.ANY_COLLECTION_NAME.*</code>
                    </div>

                    <hr class="m-t-10 m-b-5" />

                    <p>{$_("common.popup.apiDocs.content.4")}
                        <br />
                        <code>@request.auth.id != "" && created > "2022-01-01 00:00:00"</code>
                    </p>
                </div>
            </div>
        </div>
    {/if}
</div>

<RuleField
    label={$_("common.popup.apiRequestPermission.placeholder.4")}
    formKey="listRule"
    {collection}
    bind:rule={collection.listRule}
/>

<RuleField
    label={$_("common.popup.apiRequestPermission.placeholder.5")}
    formKey="viewRule"
    {collection}
    bind:rule={collection.viewRule}
/>

{#if collection?.type !== "view"}
    <RuleField
        label={$_("common.popup.apiRequestPermission.placeholder.1")}
        formKey="createRule"
        {collection}
        bind:rule={collection.createRule}
    >
        <svelte:fragment slot="afterLabel" let:isSuperuserOnly>
            {#if !isSuperuserOnly}
                <i
                    class="ri-information-line link-hint"
                    use:tooltip={{
                        text: `The Create rule is executed after a "dry save" of the submitted data, giving you access to the main record fields as in every other rule.`,
                        position: "top",
                    }}
                />
            {/if}
        </svelte:fragment>
    </RuleField>

    <RuleField
        label={$_("common.popup.apiRequestPermission.placeholder.3")}
        formKey="updateRule"
        {collection}
        bind:rule={collection.updateRule}
    />

    <RuleField
        label={$_("common.popup.apiRequestPermission.placeholder.2")}
        formKey="deleteRule"
        {collection}
        bind:rule={collection.deleteRule}
    />
{/if}

{#if collection?.type === "auth"}
    <hr />

    <button
        type="button"
        class="btn btn-sm m-b-sm {showExtraRules ? 'btn-secondary' : 'btn-hint btn-transparent'}"
        on:click={() => {
            showExtraRules = !showExtraRules;
        }}
    >
        <strong class="txt">{$_("common.popup.apiRequestPermission.placeholder.8")}</strong>
        {#if showExtraRules}
            <i class="ri-arrow-up-s-line txt-sm" />
        {:else}
            <i class="ri-arrow-down-s-line txt-sm" />
        {/if}
    </button>

    {#if showExtraRules}
        <div class="block" transition:slide={{ duration: 150 }}>
            <RuleField
                label={$_("common.popup.apiRequestPermission.placeholder.6")}
                formKey="authRule"
                placeholder=""
                {collection}
                bind:rule={collection.authRule}
            >
                <svelte:fragment>
                    <p>{$_("common.popup.apiRequestPermission.content.1")}</p>
                    <p>{$_("common.popup.apiRequestPermission.content.2")}</p>
                    <p>{$_("common.popup.apiRequestPermission.content.3")}</p>
                    <p>{$_("common.popup.apiRequestPermission.content.4")}</p>
                </svelte:fragment>
            </RuleField>

            <RuleField
                label={$_("common.popup.apiRequestPermission.placeholder.7")}
                formKey="manageRule"
                placeholder=""
                required={collection.manageRule !== null}
                {collection}
                bind:rule={collection.manageRule}
            >
                <svelte:fragment>
                    <p>
                        This rule is executed in addition to the <code>create</code> and <code>update</code> API
                        rules.
                    </p>
                    <p>
                        It enables superuser-like permissions to allow fully managing the auth record(s), eg.
                        changing the password without requiring to enter the old one, directly updating the
                        verified state or email, etc.
                    </p>
                </svelte:fragment>
            </RuleField>
        </div>
    {/if}
{/if}
