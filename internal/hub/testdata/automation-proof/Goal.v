Theorem proofbridge_add_zero : forall n : nat, n + 0 = n.
Proof.
  intro n. induction n as [|n IH].
  - reflexivity.
  - simpl. rewrite IH. reflexivity.
Qed.
