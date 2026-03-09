#![cfg(feature = "native")]
use aes_gcm::{aead::{Aead, KeyInit}, Aes256Gcm, Nonce};
use openmls::{
    prelude::*,
    prelude::tls_codec::{Serialize, Deserialize},
};
use openmls_basic_credential::SignatureKeyPair;
use openmls_rust_crypto::OpenMlsRustCrypto;
use rand::RngCore;

fn generate_credential_with_key(
    identity: Vec<u8>,
    _credential_type: CredentialType, // unused now
    signature_algorithm: SignatureScheme,
    provider: &impl OpenMlsProvider,
) -> (CredentialWithKey, SignatureKeyPair) {
    let credential = BasicCredential::new(identity); // BasicCredential comes from openmls::prelude now
    let signature_keys = SignatureKeyPair::new(signature_algorithm)
        .expect("Error generating a signature key pair.");
    signature_keys
        .store(provider.storage())
        .expect("Error storing signature keys in key store.");
    (
        CredentialWithKey {
            credential: credential.into(),
            signature_key: signature_keys.public().into(),
        },
        signature_keys,
    )
}

fn generate_key_package(
    ciphersuite: Ciphersuite,
    provider: &impl OpenMlsProvider,
    signer: &SignatureKeyPair,
    credential_with_key: CredentialWithKey,
) -> KeyPackageBundle {
    KeyPackage::builder()
        .build(ciphersuite, provider, signer, credential_with_key)
        .unwrap()
}

fn export_app_secret(group: &MlsGroup, provider: &OpenMlsRustCrypto, label: &str) -> Vec<u8> {
    group.export_secret(provider.crypto(), label, &[], 32).unwrap()
}

fn aes_gcm_encrypt(key: &[u8], plaintext: &[u8]) -> (Vec<u8>, [u8; 12]) {
    let cipher = Aes256Gcm::new_from_slice(key).unwrap();
    let mut nonce_bytes = [0u8; 12];
    rand::thread_rng().fill_bytes(&mut nonce_bytes);
    let ct = cipher.encrypt(Nonce::from_slice(&nonce_bytes), plaintext).unwrap();
    (ct, nonce_bytes)
}

fn aes_gcm_decrypt(key: &[u8], ciphertext: &[u8], nonce: &[u8; 12]) -> Vec<u8> {
    let cipher = Aes256Gcm::new_from_slice(key).unwrap();
    cipher.decrypt(Nonce::from_slice(nonce), ciphertext).unwrap()
}

fn merge_processed_commit(
    group: &mut MlsGroup,
    provider: &OpenMlsRustCrypto,
    processed: ProcessedMessage,
) {
    match processed.into_content() {
        ProcessedMessageContent::StagedCommitMessage(commit) => {
            group.merge_staged_commit(provider, *commit).unwrap();
        }
        other => panic!("expected commit message, got {:?}", other),
    }
}

fn msg_out_to_protocol(msg: &MlsMessageOut) -> ProtocolMessage {
    let bytes = msg.tls_serialize_detached().unwrap();
    let mls_in = MlsMessageIn::tls_deserialize(&mut bytes.as_slice()).unwrap();
    mls_in.try_into_protocol_message().unwrap()
}

fn main() {
    let ciphersuite = Ciphersuite::MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519;
    let provider_sasha = OpenMlsRustCrypto::default();
    let provider_maxim = OpenMlsRustCrypto::default();
    let provider_charlie = OpenMlsRustCrypto::default();

    // Credentials
    let (sasha_ck, sasha_signer) = generate_credential_with_key(
        b"Sasha".to_vec(),
        CredentialType::Basic,
        ciphersuite.signature_algorithm(),
        &provider_sasha,
    );
    let (maxim_ck, maxim_signer) = generate_credential_with_key(
        b"Maxim".to_vec(),
        CredentialType::Basic,
        ciphersuite.signature_algorithm(),
        &provider_maxim,
    );

    // Key package for Maxim
    let maxim_kpb = generate_key_package(ciphersuite, &provider_maxim, &maxim_signer, maxim_ck);

    // Sasha creates group and adds Maxim
    let mut sasha_group = MlsGroup::new(
        &provider_sasha,
        &sasha_signer,
        &MlsGroupCreateConfig::default(),
        sasha_ck,
    ).expect("create group");
    let (_msg, welcome_out, _gi) = sasha_group
        .add_members(&provider_sasha, &sasha_signer, core::slice::from_ref(maxim_kpb.key_package()))
        .expect("add members");
    sasha_group.merge_pending_commit(&provider_sasha).expect("merge pending");

    // Send welcome to Maxim
    let serialized_welcome = welcome_out.tls_serialize_detached().unwrap();
    let mls_in = MlsMessageIn::tls_deserialize(&mut serialized_welcome.as_slice()).unwrap();
    let welcome = match mls_in.extract() {
    MlsMessageBodyIn::Welcome(welcome) => welcome,
    _ => unreachable!("Unexpected message type."),
    };

    // Maxim stages join and builds group
    let staged = StagedWelcome::new_from_welcome(
        &provider_maxim,
        &MlsGroupJoinConfig::default(),
        welcome,
        Some(sasha_group.export_ratchet_tree().into()),
    ).expect("staged join");
    let mut maxim_group = staged.into_group(&provider_maxim).expect("into group");

    // Export shared secret and use it directly
    let k_sasha = export_app_secret(&sasha_group, &provider_sasha, "app-key");
    let k_maxim = export_app_secret(&maxim_group, &provider_maxim, "app-key");
    assert_eq!(k_sasha, k_maxim);

    let (ct, nonce) = aes_gcm_encrypt(&k_sasha, b"hello");
    let pt = aes_gcm_decrypt(&k_maxim, &ct, &nonce);
    println!("Decrypted: {}", String::from_utf8_lossy(&pt));
    
    // Save baseline key
    let k0 = export_app_secret(&sasha_group, &provider_sasha, "app-key");
    assert_eq!(k0, export_app_secret(&maxim_group, &provider_maxim, "app-key"));

    // Add Charlie
    let (charlie_ck, charlie_signer) = generate_credential_with_key(
        b"Charlie".to_vec(),
        CredentialType::Basic,
        ciphersuite.signature_algorithm(),
        &provider_charlie,
    );
    let charlie_kpb = generate_key_package(ciphersuite, &provider_charlie, &charlie_signer, charlie_ck.clone());
    let (c_commit, c_welcome_out, _) = sasha_group
        .add_members(&provider_sasha, &sasha_signer, core::slice::from_ref(charlie_kpb.key_package()))
        .unwrap();
    sasha_group.merge_pending_commit(&provider_sasha).unwrap();

    // Existing member (Maxim) processes the add-commit
    let processed_add = maxim_group
        .process_message(&provider_maxim, msg_out_to_protocol(&c_commit))
        .unwrap();
    merge_processed_commit(&mut maxim_group, &provider_maxim, processed_add);

    // Extract Welcome for Charlie
    let c_welcome_bytes = c_welcome_out.tls_serialize_detached().unwrap();
    let c_welcome_in = MlsMessageIn::tls_deserialize(&mut c_welcome_bytes.as_slice()).unwrap();
    let c_welcome = match c_welcome_in.extract() {
        MlsMessageBodyIn::Welcome(w) => w,
        _ => unreachable!(),
    };

    let staged_c = StagedWelcome::new_from_welcome(
        &provider_charlie,
        &MlsGroupJoinConfig::default(),
        c_welcome,
        Some(sasha_group.export_ratchet_tree().into()),
    ).unwrap();
    let mut charlie_group = staged_c.into_group(&provider_charlie).unwrap();

    let k1 = export_app_secret(&sasha_group, &provider_sasha, "app-key");
    assert_ne!(k1, k0);
    assert_eq!(k1, export_app_secret(&maxim_group, &provider_maxim, "app-key"));
    assert_eq!(k1, export_app_secret(&charlie_group, &provider_charlie, "app-key"));

    // Remove Maxim (index 1 if member order is Sasha=0, Maxim=1, Charlie=2)
    let (r_commit, _r_welcome, _) = sasha_group
        .remove_members(
            &provider_sasha,
            &sasha_signer,
            &[LeafNodeIndex::new(1u32)],
        )
        .unwrap();
    let r_commit_for_peers = r_commit.clone();
    sasha_group.merge_pending_commit(&provider_sasha).unwrap();

    // Maxim processes removal so it can drop out cleanly (optional for your check)
    let processed_rm = maxim_group
        .process_message(&provider_maxim, msg_out_to_protocol(&r_commit_for_peers))
        .unwrap();
    merge_processed_commit(&mut maxim_group, &provider_maxim, processed_rm);

    // Charlie processes removal
    let processed_rm_c = charlie_group
        .process_message(&provider_charlie, msg_out_to_protocol(&r_commit_for_peers))
        .unwrap();
    merge_processed_commit(&mut charlie_group, &provider_charlie, processed_rm_c);

    let k2 = export_app_secret(&sasha_group, &provider_sasha, "app-key");
    assert_ne!(k2, k1);
    assert_eq!(k2, export_app_secret(&charlie_group, &provider_charlie, "app-key"));
}
