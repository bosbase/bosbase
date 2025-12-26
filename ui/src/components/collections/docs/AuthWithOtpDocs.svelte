<script>
    import { _ } from "svelte-i18n";
    import AuthWithOtpApiAuthDocs from "@/components/collections/docs/AuthWithOtpApiAuthDocs.svelte";
    import AuthWithOtpApiRequestDocs from "@/components/collections/docs/AuthWithOtpApiRequestDocs.svelte";
    import SdkTabs from "@/components/base/SdkTabs.svelte";
    import ApiClient from "@/utils/ApiClient";
    import CommonHelper from "@/utils/CommonHelper";

    export let collection;

    const apiTabs = [
        { title: "OTP Request", component: AuthWithOtpApiRequestDocs },
        { title: "OTP Auth", component: AuthWithOtpApiAuthDocs },
    ];

    let activeApiTab = 0;

    $: backendAbsUrl = CommonHelper.getApiExampleUrl(ApiClient.baseURL);
</script>

<h3 class="m-b-sm">
    {$_("common.popup.apiDocs.authWithOTP.name")}
</h3>
<div class="content txt-lg m-b-sm">
    <p>{$_("common.popup.apiDocs.authWithOTP.content.1", { values: { tableName: collection.name } })}</p>
    <p>
        Note that when requesting an OTP we return an <code>otpId</code> even if a user with the provided email
        doesn't exist as a very basic enumeration protection.
    </p>
</div>

<SdkTabs
    js={`
        import BosBase from 'bosbase';

        const pb = new BosBase('${backendAbsUrl}');

        ...

        // send OTP email to the provided auth record
        const req = await pb.collection('${collection?.name}').requestOTP('test@example.com');

        // ... show a screen/popup to enter the password from the email ...

        // authenticate with the requested OTP id and the email password
        const authData = await pb.collection('${collection?.name}').authWithOTP(
            req.otpId,
            "YOUR_OTP",
        );

        // after the above you can also access the auth data from the authStore
        console.log(pb.authStore.isValid);
        console.log(pb.authStore.token);
        console.log(pb.authStore.record.id);

        // "logout"
        pb.authStore.clear();
    `}
    dart={`
        import 'package:bosbase/bosbase.dart';

        final pb = BosBase('${backendAbsUrl}');

        ...

        // send OTP email to the provided auth record
        final req = await pb.collection('${collection?.name}').requestOTP('test@example.com');

        // ... show a screen/popup to enter the password from the email ...

        // authenticate with the requested OTP id and the email password
        final authData = await pb.collection('${collection?.name}').authWithOTP(
            req.otpId,
            "YOUR_OTP",
        );

        // after the above you can also access the auth data from the authStore
        print(pb.authStore.isValid);
        print(pb.authStore.token);
        print(pb.authStore.record.id);

        // "logout"
        pb.authStore.clear();
    `}
/>

<h6 class="m-b-xs">{$_("common.placeholder.apiUrl")}</h6>
<div class="tabs">
    <div class="tabs-header compact">
        {#each apiTabs as tab, i}
            <button class="tab-item" class:active={activeApiTab == i} on:click={() => (activeApiTab = i)}>
                <div class="txt">{tab.title}</div>
            </button>
        {/each}
    </div>
    <div class="tabs-content">
        {#each apiTabs as tab, i}
            <div class="tab-item" class:active={activeApiTab == i}>
                <svelte:component this={tab.component} {collection} />
            </div>
        {/each}
    </div>
</div>
